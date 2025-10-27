// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package cadvisor // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor"

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/google/cadvisor/cache/memory"
	cadvisormetrics "github.com/google/cadvisor/container"
	"github.com/google/cadvisor/container/containerd"
	"github.com/google/cadvisor/container/crio"
	"github.com/google/cadvisor/container/docker"
	"github.com/google/cadvisor/container/systemd"
	cInfo "github.com/google/cadvisor/info/v1"
	"github.com/google/cadvisor/manager"
	"github.com/google/cadvisor/utils/sysfs"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
)

// The amount of time for which to keep stats in memory.
const statsCacheDuration = 15 * time.Minute

// Max collection interval, it is not meaningful if allowDynamicHousekeeping = false
var maxHousekeepingInterval = 15 * time.Second

// When allowDynamicHousekeeping is true, the collection interval is floating between 1s(default) to maxHousekeepingInterval
var allowDynamicHousekeeping = true

const defaultHousekeepingInterval = 10 * time.Second

// define the interface for cadvisor manager so that we can mock it in tests.
// For detailed information about these APIs, see https://github.com/google/cadvisor/blob/release-v0.39/manager/manager.go
type cadvisorManager interface {
	// Start the manager. Calling other manager methods before this returns
	// may produce undefined behavior.
	Start() error

	// Get information about all subcontainers of the specified container (includes self).
	SubcontainersInfo(containerName string, query *cInfo.ContainerInfoRequest) ([]*cInfo.ContainerInfo, error)
}

// define a function type for creating a cadvisor manager
type createCadvisorManager func(*memory.InMemoryCache, sysfs.SysFs, manager.HousekeepingConfig, cadvisormetrics.MetricSet, *http.Client,
	[]string, string) (cadvisorManager, error)

// this is the default function that are used in production code to create a cadvisor manager
// We define defaultCreateManager and use it as a field value for Cadvisor mainly for the purpose of
// unit testing. Ideally we should just replace the cadvisor manager with a mock in unit tests instead of calling
// createCadvisorManager() to create a mock. However this means that we will not be able
// to unit test the code in `initManager(...)`. For now, I will leave code as it is. Hopefully we can find
// a better way to mock the cadvisor related part in the future.
var defaultCreateManager = func(memoryCache *memory.InMemoryCache, sysfs sysfs.SysFs, housekeepingConfig manager.HousekeepingConfig,
	includedMetricsSet cadvisormetrics.MetricSet, collectorHTTPClient *http.Client, rawContainerCgroupPathPrefixWhiteList []string,
	perfEventsFile string,
) (cadvisorManager, error) {
	return manager.New(memoryCache, sysfs, housekeepingConfig, includedMetricsSet, collectorHTTPClient, rawContainerCgroupPathPrefixWhiteList, []string{}, perfEventsFile, 0)
}

// Option is a function that can be used to configure Cadvisor struct
type Option func(*Cadvisor)

func cadvisorManagerCreator(f createCadvisorManager) Option {
	return func(c *Cadvisor) {
		c.createCadvisorManager = f
	}
}

// WithDecorator constructs an option for configuring the metric decorator
func WithDecorator(d Decorator) Option {
	return func(c *Cadvisor) {
		c.decorator = d
	}
}

func WithECSInfoCreator(f EcsInfo) Option {
	return func(c *Cadvisor) {
		c.ecsInfo = f
	}
}

type hostInfo interface {
	GetNumCores() int64
	GetMemoryCapacity() int64
	GetClusterName() string
}

type EcsInfo interface {
	GetCPUReserved() int64
	GetMemReserved() int64
	GetRunningTaskCount() int64
}

type Decorator interface {
	Decorate(stores.CIMetric) stores.CIMetric
	Shutdown() error
}

type Cadvisor struct {
	logger                *zap.Logger
	createCadvisorManager createCadvisorManager
	manager               cadvisorManager
	hostInfo              hostInfo
	decorator             Decorator
	ecsInfo               EcsInfo
	containerOrchestrator string
	metricsExtractors     []extractors.MetricExtractor
	lastSocketInode       uint64
	socketPath            string
	periodicCheckCounter  int
}

func init() {
	// Override the default cAdvisor housekeeping interval.
	// We should use a proper way to configure once the issue is resolved: https://github.com/google/cadvisor/issues/2886
	*manager.HousekeepingInterval = defaultHousekeepingInterval
}

// New creates a Cadvisor struct which can generate metrics from embedded cadvisor lib
func New(containerOrchestrator string, hostInfo hostInfo, logger *zap.Logger, options ...Option) (*Cadvisor, error) {
	c := &Cadvisor{
		logger:                logger,
		createCadvisorManager: defaultCreateManager,
		containerOrchestrator: containerOrchestrator,
	}

	// apply additional options
	for _, option := range options {
		option(c)
	}

	if err := c.initManager(c.createCadvisorManager); err != nil {
		return nil, err
	}

	c.hostInfo = hostInfo
	return c, nil
}

func (c *Cadvisor) GetMetricsExtractors() []extractors.MetricExtractor {
	return c.metricsExtractors
}

func (c *Cadvisor) Shutdown() error {
	var errs error
	for _, ext := range c.metricsExtractors {
		errs = errors.Join(errs, ext.Shutdown())
	}

	return errs
}

func (c *Cadvisor) addECSMetrics(cadvisormetrics []stores.CIMetric) {
	if len(cadvisormetrics) == 0 {
		c.logger.Warn("cadvisor can't collect any metrics!")
	}

	for _, cadvisormetric := range cadvisormetrics {
		if cadvisormetric.GetMetricType() == ci.TypeInstance {
			metricMap := cadvisormetric.GetFields()
			cpuReserved := c.ecsInfo.GetCPUReserved()
			memReserved := c.ecsInfo.GetMemReserved()
			if cpuReserved == 0 && memReserved == 0 {
				c.logger.Warn("Can't get mem or cpu reserved!")
			}
			cpuLimits, cpuExist := metricMap[ci.MetricName(ci.TypeInstance, ci.CPULimit)]
			memLimits, memExist := metricMap[ci.MetricName(ci.TypeInstance, ci.MemLimit)]

			if !cpuExist && !memExist {
				c.logger.Warn("Can't get mem or cpu limit")
			} else {
				// cgroup standard cpulimits should be cadvisor standard * 1.024
				metricMap[ci.MetricName(ci.TypeInstance, ci.CPUReservedCapacity)] = float64(cpuReserved) / (float64(cpuLimits.(int64)) * 1.024) * 100
				metricMap[ci.MetricName(ci.TypeInstance, ci.MemReservedCapacity)] = float64(memReserved) / float64(memLimits.(int64)) * 100
			}

			if c.ecsInfo.GetRunningTaskCount() == 0 {
				c.logger.Warn("Can't get running task number")
			} else {
				metricMap[ci.MetricName(ci.TypeInstance, ci.RunningTaskCount)] = c.ecsInfo.GetRunningTaskCount()
			}
		}
	}
}

// GetMetrics generates metrics from cadvisor
func (c *Cadvisor) GetMetrics() []pmetric.Metrics {
	c.logger.Debug("collect data from cadvisor...")
	var result []pmetric.Metrics
	var containerinfos []*cInfo.ContainerInfo
	var err error

	// For EKS don't emit metrics if the cluster name is not detected
	if c.containerOrchestrator == ci.EKS {
		clusterName := c.hostInfo.GetClusterName()
		if clusterName == "" {
			c.logger.Warn("Failed to detect cluster name. Drop all metrics")
			return result
		}
	}

	req := &cInfo.ContainerInfoRequest{
		NumStats: 1,
	}

	containerinfos, err = c.manager.SubcontainersInfo("/", req)
	if err != nil {
		c.logger.Error("SubcontainersInfo failed", zap.Error(err))
		
		// Check if containerd restarted and retry once
		if c.hasContainerdRestarted() {
			c.logger.Info("Containerd restart detected, reinitializing manager")
			if reinitErr := c.initManager(c.createCadvisorManager); reinitErr == nil {
				containerinfos, err = c.manager.SubcontainersInfo("/", req)
				if err != nil {
					c.logger.Error("SubcontainersInfo still failed after restart recovery", zap.Error(err))
				} else {
					c.logger.Info("SubcontainersInfo succeeded after restart recovery")
				}
			} else {
				c.logger.Error("Failed to reinitialize manager after restart", zap.Error(reinitErr))
			}
		} else {
			c.logger.Info("Error occurred but containerd restart not detected")
		}

		if err != nil {
			c.logger.Warn("GetContainerInfo failed", zap.Error(err))
			return result
		}
	} else {
		// Even if no error, periodically check for restart (every 10th call)
		// This helps catch cases where containerd restarted but cadvisor hasn't failed yet
		if c.shouldPerformPeriodicRestartCheck() {
			c.logger.Info("RESTART CHECK")
			if c.hasContainerdRestarted() {
				c.logger.Info("Containerd restart detected during periodic check, reinitializing manager")
				if reinitErr := c.initManager(c.createCadvisorManager); reinitErr != nil {
					c.logger.Error("Failed to reinitialize manager during periodic check", zap.Error(reinitErr))
				}
			}
		}
	}

	out := processContainers(containerinfos, c.hostInfo, c.containerOrchestrator, c.logger, c.GetMetricsExtractors())
	var results []stores.CIMetric
	for _, m := range out {
		results = append(results, c.decorator.Decorate(m))
	}

	if c.containerOrchestrator == ci.ECS {
		c.addECSMetrics(results)
	}

	for _, cadvisorMetric := range results {
		if cadvisorMetric == nil {
			continue
		}
		md := ci.ConvertToOTLPMetrics(cadvisorMetric.GetFields(), cadvisorMetric.GetTags(), c.logger)
		result = append(result, md)
	}

	return result
}

// initManager accepts a function of type createCadvisorManager which can be used
// to create a cadvisor manager
func (c *Cadvisor) initManager(createManager createCadvisorManager) error {
	sysFs := sysfs.NewRealSysFs()
	includedMetrics := cadvisormetrics.MetricSet{
		cadvisormetrics.CpuUsageMetrics:     struct{}{},
		cadvisormetrics.MemoryUsageMetrics:  struct{}{},
		cadvisormetrics.DiskIOMetrics:       struct{}{},
		cadvisormetrics.NetworkUsageMetrics: struct{}{},
		cadvisormetrics.DiskUsageMetrics:    struct{}{},
	}
	var cgroupRoots []string
	if c.containerOrchestrator == ci.EKS {
		cgroupRoots = []string{"/kubepods"}
	}

	houseKeepingConfig := manager.HousekeepingConfig{
		Interval:     &maxHousekeepingInterval,
		AllowDynamic: &allowDynamicHousekeeping,
	}
	// Create and start the cAdvisor container manager.
	m, err := createManager(memory.New(statsCacheDuration, nil), sysFs, houseKeepingConfig, includedMetrics, http.DefaultClient, cgroupRoots, "")
	if err != nil {
		c.logger.Error("cadvisor manager allocate failed, ", zap.Error(err))
		return err
	}
	_ = cadvisormetrics.RegisterPlugin("containerd", containerd.NewPlugin())
	_ = cadvisormetrics.RegisterPlugin("crio", crio.NewPlugin())
	_ = cadvisormetrics.RegisterPlugin("docker", docker.NewPlugin())
	_ = cadvisormetrics.RegisterPlugin("systemd", systemd.NewPlugin())
	c.manager = m
	err = c.manager.Start()
	if err != nil {
		c.logger.Error("cadvisor manager start failed", zap.Error(err))
		return err
	}

	c.metricsExtractors = make([]extractors.MetricExtractor, 0, 5)
	c.metricsExtractors = append(c.metricsExtractors, extractors.NewCPUMetricExtractor(c.logger))
	c.metricsExtractors = append(c.metricsExtractors, extractors.NewMemMetricExtractor(c.logger))
	c.metricsExtractors = append(c.metricsExtractors, extractors.NewDiskIOMetricExtractor(c.logger))
	c.metricsExtractors = append(c.metricsExtractors, extractors.NewNetMetricExtractor(c.logger))
	c.metricsExtractors = append(c.metricsExtractors, extractors.NewFileSystemMetricExtractor(c.logger))

	return nil
}

// hasContainerdRestarted checks if containerd has restarted using multiple detection methods
func (c *Cadvisor) hasContainerdRestarted() bool {
	c.logger.Info("=== CONTAINERD RESTART CHECK START ===")
	
	// Check multiple socket paths that containerd might use
	socketPaths := []string{
		"/run/containerd/containerd.sock",
		"/var/run/containerd/containerd.sock",
		"/run/docker/containerd/containerd.sock",
	}

	// If we have a specific socket path, prioritize it
	if c.socketPath != "" {
		socketPaths = append([]string{c.socketPath}, socketPaths...)
	} else {
		c.socketPath = socketPaths[0] // Default to first path
	}

	c.logger.Info("Checking containerd restart status", zap.Strings("socket_paths", socketPaths))

	// Method 1: Check if any socket is accessible
	socketAccessible := false
	var workingSocket string
	for _, path := range socketPaths {
		if c.isSocketAccessible(path) {
			socketAccessible = true
			workingSocket = path
			c.socketPath = path // Update to working socket
			break
		}
	}

	if !socketAccessible {
		c.logger.Info("No containerd socket accessible - assuming restart needed")
		c.logger.Info("=== CONTAINERD RESTART CHECK END (no accessible socket) ===")
		return true
	}

	c.logger.Info("Found accessible socket", zap.String("socket_path", workingSocket))

	// Method 2: Check inode changes (original method)
	stat, err := os.Stat(workingSocket)
	if err != nil {
		c.logger.Error("Failed to stat containerd socket", 
			zap.String("socket_path", workingSocket),
			zap.Error(err))
		c.logger.Info("=== CONTAINERD RESTART CHECK END (stat failed) ===")
		return true // Assume restart if we can't stat
	}

	// Get inode number (works on Linux)
	if sysStat, ok := stat.Sys().(*syscall.Stat_t); ok {
		currentInode := sysStat.Ino
		c.logger.Info("Socket inode information",
			zap.Uint64("current_inode", currentInode),
			zap.Uint64("last_inode", c.lastSocketInode))

		if c.lastSocketInode == 0 {
			c.logger.Info("First time checking socket, storing inode")
			c.lastSocketInode = currentInode
			c.logger.Info("=== CONTAINERD RESTART CHECK END (first check) ===")
			return false
		}

		if currentInode != c.lastSocketInode {
			c.logger.Info("CONTAINERD RESTART DETECTED - inode changed!",
				zap.Uint64("old_inode", c.lastSocketInode),
				zap.Uint64("new_inode", currentInode))
			c.lastSocketInode = currentInode
			c.logger.Info("=== CONTAINERD RESTART CHECK END (restart detected) ===")
			return true
		}

		c.logger.Info("Socket inode unchanged")
	}

	// Method 3: Check if containerd process is running
	if !c.isContainerdRunning() {
		c.logger.Info("CONTAINERD RESTART DETECTED - process not running!")
		c.logger.Info("=== CONTAINERD RESTART CHECK END (process not running) ===")
		return true
	}

	// Method 4: Test actual connectivity to the socket
	if !c.canConnectToSocket(workingSocket) {
		c.logger.Info("CONTAINERD RESTART DETECTED - cannot connect to socket!")
		c.logger.Info("=== CONTAINERD RESTART CHECK END (connection failed) ===")
		return true
	}

	c.logger.Info("All checks passed - no restart detected")
	c.logger.Info("=== CONTAINERD RESTART CHECK END (no restart) ===")
	return false
}

// isSocketAccessible checks if a socket file exists and has proper permissions
func (c *Cadvisor) isSocketAccessible(socketPath string) bool {
	stat, err := os.Stat(socketPath)
	if err != nil {
		c.logger.Debug("Socket not accessible", zap.String("path", socketPath), zap.Error(err))
		return false
	}

	// Check if it's actually a socket
	if stat.Mode()&os.ModeSocket == 0 {
		c.logger.Debug("File is not a socket", zap.String("path", socketPath))
		return false
	}

	c.logger.Debug("Socket is accessible", zap.String("path", socketPath))
	return true
}

// isContainerdRunning checks if containerd process is running
func (c *Cadvisor) isContainerdRunning() bool {
	cmd := exec.Command("pgrep", "-f", "containerd")
	output, err := cmd.Output()
	if err != nil {
		c.logger.Debug("Failed to check containerd process", zap.Error(err))
		return false
	}

	pids := strings.TrimSpace(string(output))
	isRunning := pids != ""
	c.logger.Debug("Containerd process check", zap.Bool("running", isRunning), zap.String("pids", pids))
	return isRunning
}

// canConnectToSocket tests if we can actually connect to the socket
func (c *Cadvisor) canConnectToSocket(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		c.logger.Debug("Cannot connect to socket", zap.String("path", socketPath), zap.Error(err))
		return false
	}
	defer conn.Close()
	
	c.logger.Debug("Successfully connected to socket", zap.String("path", socketPath))
	return true
}
// shouldPerformPeriodicRestartCheck determines if we should check for restart even when no error occurred
// This helps catch cases where containerd restarted but cadvisor hasn't failed yet
func (c *Cadvisor) shouldPerformPeriodicRestartCheck() bool {
	c.periodicCheckCounter++
	// Check every 10th call (adjust frequency as needed)
	if c.periodicCheckCounter >= 1 {
		c.periodicCheckCounter = 0
		return true
	}
	return false
}