// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package efa // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/efa"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/metrics"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
)

const (
	defaultCollectionInterval = 20 * time.Second
)

const (
	efaPath            = "/sys/class/infiniband"
	efaK8sResourceName = "vpc.amazonaws.com/efa"

	// hardware counter names
	counterRdmaReadBytes      = "rdma_read_bytes"
	counterRdmaWriteBytes     = "rdma_write_bytes"
	counterRdmaWriteRecvBytes = "rdma_write_recv_bytes"
	counterRxBytes            = "rx_bytes"
	counterRxDrops            = "rx_drops"
	counterTxBytes            = "tx_bytes"
)

var counterNames = map[string]any{
	counterRdmaReadBytes:      nil,
	counterRdmaWriteBytes:     nil,
	counterRdmaWriteRecvBytes: nil,
	counterRxBytes:            nil,
	counterRxDrops:            nil,
	counterTxBytes:            nil,
}

type Scraper struct {
	collectionInterval time.Duration
	cancel             context.CancelFunc

	sysFsReader       sysFsReader
	deltaCalculator   metrics.MetricCalculator
	decorator         stores.Decorator
	podResourcesStore podResourcesStore
	store             *efaStore
	logger            *zap.Logger
}

type sysFsReader interface {
	EfaDataExists() (bool, error)
	ListDevices() ([]efaDeviceName, error)
	ListPorts(deviceName efaDeviceName) ([]string, error)
	ReadCounter(deviceName efaDeviceName, port string, counter string) (uint64, error)
}

type podResourcesStore interface {
	AddResourceName(resourceName string)
	GetContainerInfo(deviceID string, resourceName string) *stores.ContainerInfo
}

type efaStore struct {
	timestamp time.Time
	devices   *efaDevices
}

// efaDevices is a collection of every Amazon Elastic Fabric Adapter (EFA) device in
// /sys/class/infiniband.
type efaDevices map[efaDeviceName]*efaCounters

type efaDeviceName string

// efaCounters contains counter values from files in
// /sys/class/infiniband/<Name>/ports/<Port>/hw_counters
// for a single port of one Amazon Elastic Fabric Adapter device.
type efaCounters struct {
	rdmaReadBytes      uint64 // hw_counters/rdma_read_bytes
	rdmaWriteBytes     uint64 // hw_counters/rdma_write_bytes
	rdmaWriteRecvBytes uint64 // hw_counters/rdma_write_recv_bytes
	rxBytes            uint64 // hw_counters/rx_bytes
	rxDrops            uint64 // hw_counters/rx_drops
	txBytes            uint64 // hw_counters/tx_bytes
}

func NewEfaSyfsScraper(logger *zap.Logger, decorator stores.Decorator, podResourcesStore podResourcesStore) *Scraper {
	logger.Debug("NewEfaSyfsScraper")
	ctx, cancel := context.WithCancel(context.Background())
	podResourcesStore.AddResourceName(efaK8sResourceName)
	e := &Scraper{
		collectionInterval: defaultCollectionInterval,
		cancel:             cancel,
		sysFsReader:        defaultSysFsReader(logger),
		deltaCalculator:    metrics.NewMetricCalculator(calculateDelta),
		decorator:          decorator,
		podResourcesStore:  podResourcesStore,
		store:              new(efaStore),
		logger:             logger,
	}

	go e.startScrape(ctx)

	return e
}

func calculateDelta(prev *metrics.MetricValue, val any, _ time.Time) (any, bool) {
	if prev == nil {
		return 0, false
	}
	return val.(uint64) - prev.RawValue.(uint64), true
}

func (s *Scraper) Shutdown() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Scraper) GetMetrics() []pmetric.Metrics {
	var result []pmetric.Metrics

	store := s.store
	s.logger.Debug("going to populate metrics from devices", zap.Int("numDevices", len(*store.devices)))
	for deviceName, counters := range *store.devices {
		containerInfo := s.podResourcesStore.GetContainerInfo(string(deviceName), efaK8sResourceName)
		s.logger.Debug("containerInfo for device", zap.String("deviceName", string(deviceName)), zap.Any("containerInfo", containerInfo))

		// so container and pod should use the containerInfo for the delta cache no matter what, because you wouldn't
		// want to accidentally include usage from

		containerMetric := stores.NewCIMetric(ci.TypeContainerEFA, s.logger)
		podMetric := stores.NewCIMetric(ci.TypePodEFA, s.logger)
		nodeMetric := stores.NewCIMetric(ci.TypeNodeEFA, s.logger)

		nodeKey := metrics.Key{
			MetricMetadata: metadata{
				measurement: metricName,
			},
		}

		s.fillMetric(containerMetric, ci.TypeContainerEFA, containerInfo.Namespace, containerInfo.PodName,
			containerInfo.ContainerName, string(deviceName), store.timestamp, counters)
		s.fillMetric(podMetric, ci.TypePodEFA, containerInfo.Namespace, containerInfo.PodName,
			containerInfo.ContainerName, string(deviceName), store.timestamp, counters)
		s.fillMetric(nodeMetric, ci.TypeNodeEFA, containerInfo.Namespace, containerInfo.PodName,
			containerInfo.ContainerName, string(deviceName), store.timestamp, counters)

		for _, m := range []stores.CIMetric{containerMetric, podMetric, nodeMetric} {
			m.AddTag(ci.AttributeEfaDevice, string(deviceName))
			m.AddTag(ci.Timestamp, strconv.FormatInt(store.timestamp.UnixNano(), 10))
		}
		for _, m := range []stores.CIMetric{containerMetric, podMetric} {
			m.AddTag(ci.AttributeK8sNamespace, containerInfo.Namespace)
			m.AddTag(ci.AttributeK8sPodName, containerInfo.PodName)
			m.AddTag(ci.AttributeContainerName, containerInfo.ContainerName)
		}

		for _, m := range []stores.CIMetric{containerMetric, podMetric, nodeMetric} {
			if len(m.GetFields()) == 0 {
				continue
			}
			metric := s.decorator.Decorate(m)
			result = append(result, ci.ConvertToOTLPMetrics(metric.GetFields(), metric.GetTags(), s.logger))
		}
	}

	s.logger.Debug("returning metrics from GetMetrics\n", zap.Int("numMetrics", len(result)))
	return result
}

type metadata struct {
	measurement       string
	deviceName        string
	containerMetadata stores.ContainerInfo
}

type containerMetadata struct {
	namespace     string
	podName       string
	containerName string
	deviceName    string
}

func (s *Scraper) fillMetric(metric stores.CIMetric, metricType string, namespace string, podName string,
	containerName string, deviceName string, timestamp time.Time, counters *efaCounters) {

	metricNameValue := map[string]uint64{
		ci.MetricName(metricType, ci.EfaRdmaReadBytes):      counters.rdmaReadBytes,
		ci.MetricName(metricType, ci.EfaRdmaWriteBytes):     counters.rdmaWriteBytes,
		ci.MetricName(metricType, ci.EfaRdmaWriteRecvBytes): counters.rdmaWriteRecvBytes,
		ci.MetricName(metricType, ci.EfaRxBytes):            counters.rxBytes,
		ci.MetricName(metricType, ci.EfaRxDropped):          counters.rxDrops,
		ci.MetricName(metricType, ci.EfaTxBytes):            counters.txBytes,
	}

	for metricName, value := range metricNameValue {
		s.assignRateValueToField(metricName, value, metric, namespace, podName, containerName, deviceName, timestamp)
	}
}

func (s *Scraper) something(metric stores.CIMetric, containerInfo *stores.ContainerInfo, deviceName string,
	timestamp time.Time, counters *efaCounters) {
	measurementValue := map[string]uint64{
		ci.EfaRdmaReadBytes:      counters.rdmaReadBytes,
		ci.EfaRdmaWriteBytes:     counters.rdmaWriteBytes,
		ci.EfaRdmaWriteRecvBytes: counters.rdmaWriteRecvBytes,
		ci.EfaRxBytes:            counters.rxBytes,
		ci.EfaRxDropped:          counters.rxDrops,
		ci.EfaTxBytes:            counters.txBytes,
	}

	for measurement, value := range measurementValue {
		nodeKey := metrics.Key{
			MetricMetadata: metadata{
				measurement: measurement,
				deviceName:  deviceName,
			},
		}
		s.fillMetric2(metric, timestamp, &nodeKey, ci.MetricName(ci.TypeNodeEFA, measurement), value)

		if containerInfo != nil {
			containerKey := metrics.Key{
				MetricMetadata: metadata{
					measurement:       measurement,
					deviceName:        deviceName,
					containerMetadata: *containerInfo,
				},
			}
			s.fillMetric2(metric, timestamp, &containerKey, ci.MetricName(ci.TypePodEFA, measurement), value)
			s.fillMetric2(metric, timestamp, &containerKey, ci.MetricName(ci.TypeContainerEFA, measurement), value)
		}
	}
}

func (s *Scraper) fillMetric2(metric stores.CIMetric, timestamp time.Time, cacheKey *metrics.Key, metricName string, metricVal uint64) {
	deltaValue, found := s.deltaCalculator.Calculate(*cacheKey, metricVal, timestamp)
	if found {
		metric.AddField(metricName, deltaValue)
	}
}

func (s *Scraper) assignRateValueToField(metricName string, value uint64, metric stores.CIMetric, namespace string,
	podName string, containerName string, deviceName string, timestamp time.Time) {
	key := metrics.Key{
		MetricMetadata: metadata{
			namespace:     namespace,
			podName:       podName,
			containerName: containerName,
			deviceName:    deviceName,
			measurement:   metricName,
		},
	}
	deltaValue, found := s.deltaCalculator.Calculate(key, value, timestamp)
	if found {
		metric.AddField(metricName, deltaValue)
	}
}

func (s *Scraper) init() {
}

func (s *Scraper) startScrape(ctx context.Context) {
	s.logger.Debug("startScrape")
	ticker := time.NewTicker(s.collectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := s.scrape()
			if err != nil {
				s.logger.Warn("Failed to scrape EFA metrics from filesystem", zap.Error(err))
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) scrape() error {
	s.logger.Debug("scrape")
	exists, err := s.sysFsReader.EfaDataExists()
	s.logger.Debug("EfaDataExists", zap.Bool("exists", exists), zap.Error(err))
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	timestamp := time.Now()

	devices, err := s.parseEfaDevices()
	if err != nil {
		return err
	}

	s.store = &efaStore{
		timestamp: timestamp,
		devices:   devices,
	}

	return nil
}

func (s *Scraper) parseEfaDevices() (*efaDevices, error) {
	deviceNames, err := s.sysFsReader.ListDevices()
	if err != nil {
		return nil, err
	}

	devices := make(efaDevices, len(deviceNames))
	for _, name := range deviceNames {
		counters, err := s.parseEfaDevice(name)
		if err != nil {
			return nil, err
		}

		devices[name] = counters
	}

	return &devices, nil
}

func (s *Scraper) parseEfaDevice(deviceName efaDeviceName) (*efaCounters, error) {
	ports, err := s.sysFsReader.ListPorts(deviceName)
	if err != nil {
		return nil, err
	}

	counters := new(efaCounters)
	for _, port := range ports {
		err := s.readCounters(deviceName, port, counters)
		if err != nil {
			return nil, err
		}
	}
	return counters, nil
}

func (s *Scraper) readCounters(deviceName efaDeviceName, port string, counters *efaCounters) error {
	var errs error
	reader := func(counter string) uint64 {
		value, err := s.sysFsReader.ReadCounter(deviceName, port, counter)
		if err != nil {
			errs = errors.Join(errs, err)
			return 0
		}
		return value
	}

	for counter := range counterNames {
		switch counter {
		case counterRdmaReadBytes:
			counters.rdmaReadBytes += reader(counter)
		case counterRdmaWriteBytes:
			counters.rdmaWriteBytes += reader(counter)
		case counterRdmaWriteRecvBytes:
			counters.rdmaWriteRecvBytes += reader(counter)
		case counterRxBytes:
			counters.rxBytes += reader(counter)
		case counterRxDrops:
			counters.rxDrops += reader(counter)
		case counterTxBytes:
			counters.txBytes += reader(counter)
		}
	}

	return errs
}

func defaultSysFsReader(logger *zap.Logger) sysFsReader {
	return &sysfsReaderImpl{
		logger: logger,
	}
}

type sysfsReaderImpl struct {
	logger *zap.Logger
}

var _ sysFsReader = (*sysfsReaderImpl)(nil)

func (r *sysfsReaderImpl) EfaDataExists() (bool, error) {
	info, err := os.Stat(efaPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("efaPath does not exist\n")
			return false, nil
		}
		fmt.Printf("got error reading efaPath: %v\n", err)
		return false, err
	}

	err = checkPermissions(info)
	if err != nil {
		r.logger.Warn("not reading from EFA directory", zap.String("path", efaPath), zap.Error(err))
		return false, nil
	}

	return true, nil
}

func (r *sysfsReaderImpl) ListDevices() ([]efaDeviceName, error) {
	dirs, err := os.ReadDir(efaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list EFA devices at %q: %w", efaPath, err)
	}

	result := make([]efaDeviceName, 0)
	fmt.Printf("found entries at efaPath: %v\n", dirs)
	for _, dir := range dirs {
		if !dir.IsDir() && dir.Type()&os.ModeSymlink == 0 {
			continue
		}
		result = append(result, efaDeviceName(dir.Name()))
	}

	return result, nil
}

func (r *sysfsReaderImpl) ListPorts(deviceName efaDeviceName) ([]string, error) {
	portsPath := filepath.Join(efaPath, string(deviceName), "ports")
	portDirs, err := os.ReadDir(portsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list EFA ports at %q: %w", portsPath, err)
	}

	fmt.Printf("found ports in EFA device %s: %v\n", string(deviceName), portDirs)
	result := make([]string, 0)
	for _, dir := range portDirs {
		if !dir.IsDir() {
			continue
		}
		result = append(result, dir.Name())
	}

	return result, nil
}

func (r *sysfsReaderImpl) ReadCounter(deviceName efaDeviceName, port string, counter string) (uint64, error) {
	path := filepath.Join(efaPath, string(deviceName), "ports", port, "hw_counters", counter)
	return readUint64ValueFromFile(path)
}

func readUint64ValueFromFile(path string) (uint64, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) || err.Error() == "operation not supported" || err.Error() == "invalid argument" {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read file %q: %w", path, err)
	}
	stringValue := strings.TrimSpace(string(bytes))

	// Ugly workaround for handling https://github.com/prometheus/node_exporter/issues/966
	// when counters are `N/A (not available)`.
	// This was already patched and submitted, see
	// https://www.spinics.net/lists/linux-rdma/msg68596.html
	// Remove this as soon as the fix lands in the enterprise distros.
	if strings.Contains(stringValue, "N/A (no PMA)") {
		return 0, nil
	}

	value, err := parseUInt64(stringValue)
	if err != nil {
		return 0, err
	}

	return value, nil
}

// Parse string to UInt64
func parseUInt64(value string) (uint64, error) {
	// A base value of zero makes ParseUint infer the correct base using the
	// string's prefix, if any.
	const base = 0
	v, err := strconv.ParseUint(value, base, 64)
	if err != nil {
		return 0, err
	}
	return v, err
}
