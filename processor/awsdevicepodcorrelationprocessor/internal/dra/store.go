// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dra // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/dra"

import (
	"context"
	"errors"
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

	// cacheSyncTimeout bounds the initial informer cache sync so a missing RBAC
	// rule (resourceclaims/resourceslices) cannot block collector startup
	// indefinitely. A healthy sync completes in well under this budget; it is
	// only reached in the failure/slow path, where it caps how long startup
	// blocks before the caller falls back to the device-plugin path (or fails
	// when DRA is the only configured path).
	cacheSyncTimeout = 15 * time.Second
)

// DeviceTypeConfig holds pre-compiled configuration for a DRA device type.
type DeviceTypeConfig struct {
	Name              string
	DeviceIDAttribute string
	DeviceIDSource    string // "datapoint" or "resource"
	DriverNames       []string
	SourceAttribute   string
	DeviceIDPattern   *regexp.Regexp
}

// driverConfig holds the pre-compiled keying config for a single driver name.
type driverConfig struct {
	pattern         *regexp.Regexp
	sourceAttribute string
}

// draDeviceKey is the lookup key for DRA-allocated devices.
type draDeviceKey struct {
	DeviceID   string
	DriverName string
}

// sliceDeviceKey identifies a device instance within a ResourceSlice pool.
type sliceDeviceKey struct {
	Driver string
	Pool   string
	Device string
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
	sliceFactory informers.SharedInformerFactory

	podInformer   cache.SharedIndexInformer
	claimInformer cache.SharedIndexInformer
	sliceInformer cache.SharedIndexInformer // non-nil only when an attribute ref is configured

	// needSlices is true when any tracked driver keys on a ResourceSlice attribute.
	needSlices bool

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
		return errors.New("node name is required: set NODE_NAME env var or use WithNodeName option")
	}

	// Build immutable trackedDrivers map from configs.
	s.trackedDrivers = make(map[string]driverConfig, len(s.configs)*2)
	for _, cfg := range s.configs {
		for _, dn := range cfg.DriverNames {
			s.trackedDrivers[dn] = driverConfig{
				pattern:         cfg.DeviceIDPattern,
				sourceAttribute: cfg.SourceAttribute,
			}
		}
		if cfg.SourceAttribute != "" {
			s.needSlices = true
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

	// The informers must outlive Start. The collector may cancel the startup
	// context once Start returns, so give the reflectors a lifetime owned by the
	// Store (rooted at context.Background()) and cancelled in Stop(). Deriving it
	// from the caller's ctx would let a post-startup cancellation silently stop
	// all reflectors while the Store keeps serving a stale map.
	runCtx, cancel := context.WithCancel(context.Background())
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
		AddFunc:    func(_ any) { s.scheduleRebuild() },
		UpdateFunc: func(_, _ any) { s.scheduleRebuild() },
		DeleteFunc: func(_ any) { s.scheduleRebuild() },
	}
	if _, err := podInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("failed to add pod event handler: %w", err)
	}

	// ResourceClaims informer — watch all namespaces.
	// On large clusters this may be significant. A future optimization could
	// filter to only namespaces with pods on this node.
	s.claimFactory = informers.NewSharedInformerFactory(s.client, 30*time.Second)
	claimInformer := s.claimFactory.Resource().V1beta1().ResourceClaims().Informer()
	s.claimInformer = claimInformer
	if _, err := claimInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("failed to add resourceclaim event handler: %w", err)
	}

	// ResourceSlices informer — only needed when a device type keys on a
	// ResourceSlice attribute (dra_device_id_attribute). Filter to this node.
	if s.needSlices {
		s.sliceFactory = informers.NewSharedInformerFactoryWithOptions(
			s.client,
			30*time.Second,
			informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = "spec.nodeName=" + s.nodeName
			}),
		)
		sliceInformer := s.sliceFactory.Resource().V1beta1().ResourceSlices().Informer()
		s.sliceInformer = sliceInformer
		if _, err := sliceInformer.AddEventHandler(handler); err != nil {
			return fmt.Errorf("failed to add resourceslice event handler: %w", err)
		}
	}

	s.podFactory.Start(runCtx.Done())
	s.claimFactory.Start(runCtx.Done())

	syncFuncs := []cache.InformerSynced{podInformer.HasSynced, claimInformer.HasSynced}
	if s.sliceInformer != nil {
		s.sliceFactory.Start(runCtx.Done())
		syncFuncs = append(syncFuncs, s.sliceInformer.HasSynced)
	}

	// Wait for the initial cache sync, but bound it. Without a deadline, missing
	// get/list/watch RBAC on resourceclaims (or resourceslices) makes the
	// reflectors retry indefinitely and WaitForCacheSync never returns, hanging
	// collector startup. The startup ctx still aborts the wait early if the
	// collector is shutting down. On failure we tear down our own reflectors and
	// return an error; the caller decides whether that is fatal (DRA-only) or a
	// non-fatal fallback to the device-plugin path.
	syncCtx, syncCancel := context.WithTimeout(ctx, cacheSyncTimeout)
	defer syncCancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), syncFuncs...) {
		cancel()
		return fmt.Errorf("DRA informer caches did not sync within %s "+
			"(verify the ServiceAccount has get/list/watch on resourceclaims"+
			" and, when dra_device_id_attribute is set, resourceslices)", cacheSyncTimeout)
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

	// Index ResourceSlice device attributes (only if any driver keys on one).
	// Key: {driver, pool, device} -> {attribute name -> string value}.
	var sliceIndex map[sliceDeviceKey]map[string]string
	if s.sliceInformer != nil {
		slices := s.sliceInformer.GetStore().List()
		sliceIndex = make(map[sliceDeviceKey]map[string]string, len(slices))
		for _, obj := range slices {
			slice, ok := obj.(*resourceapi.ResourceSlice)
			if !ok {
				continue
			}
			for _, dev := range slice.Spec.Devices {
				if dev.Basic == nil {
					continue
				}
				attrs := make(map[string]string, len(dev.Basic.Attributes))
				for qn, attr := range dev.Basic.Attributes {
					if attr.StringValue != nil {
						attrs[string(qn)] = *attr.StringValue
					}
				}
				sliceIndex[sliceDeviceKey{Driver: slice.Spec.Driver, Pool: slice.Spec.Pool.Name, Device: dev.Name}] = attrs
			}
		}
	}

	newMap := make(map[draDeviceKey]types.ContainerInfo)
	// shared tracks device keys claimed by more than one distinct pod. A device
	// can only be correlated to a single owning pod; when several pods share one
	// (a directly-referenced ResourceClaim reused by multiple pods, or consumable-
	// capacity sharing), there is no single correct owner. Rather than pick one
	// arbitrarily and emit a confidently-wrong attribution, we drop such keys so
	// the datapoint flows through unattributed. Detection is order-independent:
	// once two distinct pods hit the same key it is marked and removed below,
	// regardless of pod/claim list order.
	shared := make(map[draDeviceKey]struct{})

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

				// Resolve the source string: either the device name (default) or
				// the value of a ResourceSlice attribute when sourceAttribute is set.
				source := deviceName
				if dc.sourceAttribute != "" {
					// Skip the device if the slice or attribute is missing —
					// guessing would create a wrong correlation.
					attrs, ok := sliceIndex[sliceDeviceKey{Driver: result.Driver, Pool: result.Pool, Device: deviceName}]
					if !ok {
						s.logger.Debug("DRA device has no matching ResourceSlice, skipping attribute keying",
							zap.String("driver", result.Driver),
							zap.String("pool", result.Pool),
							zap.String("device", deviceName),
						)
						continue
					}
					val, ok := attrs[dc.sourceAttribute]
					if !ok {
						s.logger.Debug("ResourceSlice device missing referenced attribute, skipping",
							zap.String("driver", result.Driver),
							zap.String("device", deviceName),
							zap.String("attribute", dc.sourceAttribute),
						)
						continue
					}
					source = val
				}

				// Optionally apply the regex transform to the resolved source.
				lookupID := source
				if dc.pattern != nil {
					if matches := dc.pattern.FindStringSubmatch(source); len(matches) >= 2 {
						lookupID = matches[1]
					}
				}

				key := draDeviceKey{DeviceID: lookupID, DriverName: result.Driver}
				info := types.ContainerInfo{
					PodName:       pod.Name,
					ContainerName: containerName,
					Namespace:     pod.Namespace,
				}
				// A collision from a different pod means the device is shared.
				// Same-pod collisions (e.g. two containers of one pod) keep the
				// pod-level attribution and are not treated as ambiguous.
				if prev, ok := newMap[key]; ok && (prev.PodName != info.PodName || prev.Namespace != info.Namespace) {
					shared[key] = struct{}{}
				}
				newMap[key] = info
			}
		}
	}

	// Drop shared devices: no single owning pod, so emit unattributed rather than
	// misattributed. See the "Shared devices" limitation in the README.
	for key := range shared {
		delete(newMap, key)
		s.logger.Warn("DRA device claimed by multiple pods; dropping correlation to avoid misattribution",
			zap.String("driver", key.DriverName),
			zap.String("device", key.DeviceID),
		)
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
	// Directly-referenced claim: the name is authoritative.
	if podClaim.ResourceClaimName != nil {
		return *podClaim.ResourceClaimName
	}
	// Template-generated claim: the actual claim name carries a random suffix
	// (e.g. <pod>-<claim>-abc12) that cannot be reconstructed by string
	// concatenation. The resource-claim controller records the generated name in
	// the pod status; read it there rather than guessing. If the status entry is
	// not yet populated, return "" (no guess) so the caller skips this claim until
	// the next rebuild.
	if podClaim.ResourceClaimTemplateName != nil {
		for _, status := range pod.Status.ResourceClaimStatuses {
			if status.Name == podClaim.Name && status.ResourceClaimName != nil {
				return *status.ResourceClaimName
			}
		}
	}
	return ""
}
