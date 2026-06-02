// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dra // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/dra"

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/types"
)

const (
	// debounceInterval is the minimum time between consecutive rebuilds.
	// Events arriving within this window are coalesced into a single rebuild.
	debounceInterval = 500 * time.Millisecond
)

// DeviceTypeConfig holds pre-compiled configuration for a DRA device type.
type DeviceTypeConfig struct {
	Name              string
	DeviceIDAttribute string
	DeviceIDSource    string // "datapoint" or "resource"
	DriverNames       []string
	DeviceIDPattern   *regexp.Regexp // extracts metric device ID from DRA device name
}

// driverConfig holds the pre-compiled pattern for a single driver name.
type driverConfig struct {
	pattern *regexp.Regexp
}

// draDeviceKey is the lookup key for DRA-allocated devices.
type draDeviceKey struct {
	DeviceID   string
	DriverName string
}

// Store watches Pods and ResourceClaims via the K8s API and maintains
// an in-memory map of DRA device-to-pod associations.
type Store struct {
	mu          sync.RWMutex
	deviceToPod map[draDeviceKey]types.ContainerInfo

	configs        []DeviceTypeConfig
	trackedDrivers map[string]driverConfig // immutable after Start()
	nodeName       string
	logger         *zap.Logger

	client       kubernetes.Interface
	cancel       context.CancelFunc
	podFactory   informers.SharedInformerFactory
	claimFactory informers.SharedInformerFactory

	podInformer   cache.SharedIndexInformer
	claimInformer cache.SharedIndexInformer

	// debounce state
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
}

// StoreOption configures the Store.
type StoreOption func(*Store)

// WithNodeName sets the node name to filter pods.
func WithNodeName(name string) StoreOption {
	return func(s *Store) { s.nodeName = name }
}

// WithConfigs sets the DRA device type configurations.
func WithConfigs(configs []DeviceTypeConfig) StoreOption {
	return func(s *Store) { s.configs = configs }
}

// WithKubeClient sets a pre-built Kubernetes client (useful for testing).
func WithKubeClient(client kubernetes.Interface) StoreOption {
	return func(s *Store) { s.client = client }
}

// NewStore creates a new DRA correlation store.
func NewStore(logger *zap.Logger, opts ...StoreOption) *Store {
	s := &Store{
		deviceToPod: make(map[draDeviceKey]types.ContainerInfo),
		logger:      logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.nodeName == "" {
		s.nodeName = os.Getenv("NODE_NAME")
	}
	return s
}

// Start initializes the Kubernetes client, sets up informers for Pods and
// ResourceClaims, and begins watching for changes.
func (s *Store) Start(ctx context.Context) error {
	if s.nodeName == "" {
		return fmt.Errorf("node name is required: set NODE_NAME env var or use WithNodeName option")
	}

	// Build immutable trackedDrivers map from configs.
	s.trackedDrivers = make(map[string]driverConfig, len(s.configs)*2)
	for _, cfg := range s.configs {
		for _, dn := range cfg.DriverNames {
			s.trackedDrivers[dn] = driverConfig{pattern: cfg.DeviceIDPattern}
		}
	}

	if s.client == nil {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return fmt.Errorf("failed to get in-cluster config: %w", err)
		}
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		s.client = client
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Watch pods on this node only.
	s.podFactory = informers.NewSharedInformerFactoryWithOptions(
		s.client,
		30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = "spec.nodeName=" + s.nodeName
		}),
	)

	podInformer := s.podFactory.Core().V1().Pods().Informer()
	s.podInformer = podInformer

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { s.scheduleRebuild() },
		UpdateFunc: func(_, _ interface{}) { s.scheduleRebuild() },
		DeleteFunc: func(_ interface{}) { s.scheduleRebuild() },
	}
	podInformer.AddEventHandler(handler)

	// ResourceClaims informer — watch all namespaces.
	// On large clusters this may be significant. A future optimization could
	// filter to only namespaces with pods on this node.
	s.claimFactory = informers.NewSharedInformerFactory(s.client, 30*time.Second)
	claimInformer := s.claimFactory.Resource().V1beta1().ResourceClaims().Informer()
	s.claimInformer = claimInformer
	claimInformer.AddEventHandler(handler)

	s.podFactory.Start(ctx.Done())
	s.claimFactory.Start(ctx.Done())

	// Wait for initial cache sync.
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced, claimInformer.HasSynced) {
		cancel()
		return fmt.Errorf("failed to sync informer caches")
	}

	s.rebuild()
	return nil
}

// Stop shuts down the informers and cancels any pending debounce timer.
func (s *Store) Stop() {
	s.debounceMu.Lock()
	if s.debounceTimer != nil {
		s.debounceTimer.Stop()
		s.debounceTimer = nil
	}
	s.debounceMu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
}

// GetDRAContainerInfo looks up the pod/container that owns the given DRA-allocated device.
func (s *Store) GetDRAContainerInfo(deviceID string, driverName string) *types.ContainerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := draDeviceKey{DeviceID: deviceID, DriverName: driverName}
	if info, ok := s.deviceToPod[key]; ok {
		return &info
	}
	return nil
}

// scheduleRebuild coalesces rapid event bursts into a single rebuild.
func (s *Store) scheduleRebuild() {
	s.debounceMu.Lock()
	defer s.debounceMu.Unlock()

	if s.debounceTimer != nil {
		s.debounceTimer.Reset(debounceInterval)
		return
	}
	s.debounceTimer = time.AfterFunc(debounceInterval, func() {
		s.debounceMu.Lock()
		s.debounceTimer = nil
		s.debounceMu.Unlock()
		s.rebuild()
	})
}

// rebuild recomputes the full device-to-pod map from current informer state.
func (s *Store) rebuild() {
	pods := s.podInformer.GetStore().List()
	claims := s.claimInformer.GetStore().List()

	// Index claims by namespace/name for fast lookup.
	claimIndex := make(map[string]*resourceapi.ResourceClaim, len(claims))
	for _, obj := range claims {
		claim, ok := obj.(*resourceapi.ResourceClaim)
		if !ok {
			continue
		}
		claimIndex[claim.Namespace+"/"+claim.Name] = claim
	}

	newMap := make(map[draDeviceKey]types.ContainerInfo)

	for _, obj := range pods {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}

		// Build a map: claim name -> container name for this pod.
		claimToContainer := s.buildClaimToContainerMap(pod)

		// For each resource claim referenced by the pod, look up the allocation.
		for _, podClaim := range pod.Spec.ResourceClaims {
			claimName := s.resolveClaimName(pod, podClaim)
			if claimName == "" {
				continue
			}

			claim, ok := claimIndex[pod.Namespace+"/"+claimName]
			if !ok || claim.Status.Allocation == nil {
				continue
			}

			containerName := claimToContainer[podClaim.Name]

			for _, result := range claim.Status.Allocation.Devices.Results {
				dc, tracked := s.trackedDrivers[result.Driver]
				if !tracked {
					continue
				}

				deviceName := result.Device
				lookupID := deviceName
				if dc.pattern != nil {
					matches := dc.pattern.FindStringSubmatch(deviceName)
					if len(matches) >= 2 {
						lookupID = matches[1]
					}
				}

				newMap[draDeviceKey{DeviceID: lookupID, DriverName: result.Driver}] = types.ContainerInfo{
					PodName:       pod.Name,
					ContainerName: containerName,
					Namespace:     pod.Namespace,
				}
			}
		}
	}

	s.mu.Lock()
	s.deviceToPod = newMap
	s.mu.Unlock()

	s.logger.Debug("DRA store rebuilt", zap.Int("device_count", len(newMap)))
}

// buildClaimToContainerMap maps pod-level claim names to container names.
// If multiple containers reference the same claim, the first regular container wins.
func (s *Store) buildClaimToContainerMap(pod *corev1.Pod) map[string]string {
	m := make(map[string]string)
	for _, c := range pod.Spec.Containers {
		for _, claim := range c.Resources.Claims {
			if existing, exists := m[claim.Name]; exists {
				s.logger.Debug("Multiple containers reference same claim, using first",
					zap.String("pod", pod.Name),
					zap.String("claim", claim.Name),
					zap.String("winner", existing),
					zap.String("skipped", c.Name),
				)
				continue
			}
			m[claim.Name] = c.Name
		}
	}
	for _, c := range pod.Spec.InitContainers {
		for _, claim := range c.Resources.Claims {
			if _, exists := m[claim.Name]; !exists {
				m[claim.Name] = c.Name
			}
		}
	}
	return m
}

// resolveClaimName returns the actual ResourceClaim name for a pod's claim reference.
func (s *Store) resolveClaimName(pod *corev1.Pod, podClaim corev1.PodResourceClaim) string {
	if podClaim.ResourceClaimName != nil {
		return *podClaim.ResourceClaimName
	}
	// Generated name from template: <pod-name>-<claim-name>
	if podClaim.ResourceClaimTemplateName != nil {
		return pod.Name + "-" + podClaim.Name
	}
	return ""
}
