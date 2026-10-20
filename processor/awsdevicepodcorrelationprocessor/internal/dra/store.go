// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dra // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/dra"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"
)

const (
	// defaultRefreshInterval is how often the store recomputes its map from the
	// latest kubelet bindings and ResourceSlice cache.
	defaultRefreshInterval = 10 * time.Second

	// cacheSyncTimeout bounds the initial ResourceSlice informer sync so a
	// missing RBAC rule cannot block collector startup indefinitely. A healthy
	// sync completes well under this budget; it is only reached in the
	// failure/slow path, where it caps how long startup blocks before the caller
	// falls back to the device-plugin path (or fails when DRA is the only
	// configured path).
	cacheSyncTimeout = 15 * time.Second

	// claimGetTimeout bounds each targeted ResourceClaim Get used to resolve a
	// claim's allocated devices.
	claimGetTimeout = 10 * time.Second
)

// DRABindingLister is the source of node-local, active-only DRA device-to-pod
// bindings. It is satisfied by the kubelet pod-resources client.
type DRABindingLister interface {
	ListDRADevices() []podinfo.DRABinding
}

// DeviceTypeConfig holds the keying configuration for a DRA device type.
type DeviceTypeConfig struct {
	Name string
	// SourceAttribute, when set, names a ResourceSlice device attribute whose
	// value is used as the correlation key instead of the DRA device name. This
	// bridges drivers whose device name does not match the metric label (e.g.
	// EFA via dra.net, Neuron deviceId). When empty, the device name is used.
	SourceAttribute string
	DriverNames     []string
}

// driverConfig holds the pre-compiled keying config for a single driver name.
type driverConfig struct {
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

// Store maintains an in-memory map of DRA device-to-pods associations.
//
// Device-to-pod bindings come from the node-local kubelet
// Pod Resources API (active-only), not from cluster-wide Pod/ResourceClaim
// informers. A node-scoped ResourceSlice informer provides the attribute
// bridge. A device used by multiple pods maps to all of them; the processor
// fans the metric out per pod, so no owner is dropped.
// The map is recomputed on a ticker from the latest bindings.
type Store struct {
	mu          sync.RWMutex
	deviceToPod map[draDeviceKey][]podinfo.ContainerInfo

	configs        []DeviceTypeConfig
	trackedDrivers map[string]driverConfig // immutable after Start()
	needSlices     bool
	nodeName       string
	logger         *zap.Logger

	source          DRABindingLister
	refreshInterval time.Duration

	client       kubernetes.Interface
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	sliceFactory informers.SharedInformerFactory
	// sliceInformer is non-nil only when a device type keys on a ResourceSlice
	// attribute.
	sliceInformer cache.SharedIndexInformer

	// rebuildMu serializes rebuilds (the poll ticker is the only caller today,
	// but it also guards claimCache).
	rebuildMu sync.Mutex
	// claimCache memoizes a claim's allocation (device results) per
	// "namespace/name". A claim's allocation does not change over its lifetime, so
	// entries are kept until the claim is no longer referenced by any active
	// binding. Only successful Gets are cached.
	claimCache map[string]claimInfo
}

// deviceResult is one allocated device from a ResourceClaim's allocation.
type deviceResult struct {
	driver string
	pool   string
	device string
}

// claimInfo is the memoized allocation of a ResourceClaim.
type claimInfo struct {
	results []deviceResult
}

// StoreOption configures the Store.
type StoreOption func(*Store)

// WithNodeName sets the node name used to scope the ResourceSlice informer.
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

// WithBindingSource sets the DRA binding source (the kubelet pod-resources client).
func WithBindingSource(src DRABindingLister) StoreOption {
	return func(s *Store) { s.source = src }
}

// NewStore creates a new DRA correlation store.
func NewStore(logger *zap.Logger, opts ...StoreOption) *Store {
	s := &Store{
		deviceToPod:     make(map[draDeviceKey][]podinfo.ContainerInfo),
		claimCache:      make(map[string]claimInfo),
		refreshInterval: defaultRefreshInterval,
		logger:          logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.nodeName == "" {
		s.nodeName = os.Getenv("NODE_NAME")
	}
	return s
}

// Start sets up the ResourceSlice informer (when needed) and begins the
// periodic rebuild loop that keys the latest kubelet bindings into the
// device-to-pod map.
func (s *Store) Start(ctx context.Context) error {
	if s.source == nil {
		return errors.New("DRA binding source is required: use WithBindingSource")
	}

	// Build immutable trackedDrivers map from configs.
	s.trackedDrivers = make(map[string]driverConfig, len(s.configs)*2)
	for _, cfg := range s.configs {
		for _, dn := range cfg.DriverNames {
			s.trackedDrivers[dn] = driverConfig{sourceAttribute: cfg.SourceAttribute}
		}
		if cfg.SourceAttribute != "" {
			s.needSlices = true
		}
	}

	if s.needSlices && s.nodeName == "" {
		return errors.New("node name is required when a dra_device_id_attribute is set: " +
			"set NODE_NAME env var or use WithNodeName option")
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

	// The informer must outlive Start. The collector may cancel the startup
	// context once Start returns, so give the reflector a lifetime owned by the
	// Store (rooted at context.Background()) and cancelled in Stop(). Deriving it
	// from the caller's ctx would let a post-startup cancellation silently stop
	// the reflector while the Store keeps serving a stale map.
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	// ResourceSlices informer — only needed when a device type keys on a
	// ResourceSlice attribute (dra_device_id_attribute). Scope it to this node.
	if s.needSlices {
		s.sliceFactory = informers.NewSharedInformerFactoryWithOptions(
			s.client,
			30*time.Second,
			informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = "spec.nodeName=" + s.nodeName
			}),
		)
		s.sliceInformer = s.sliceFactory.Resource().V1().ResourceSlices().Informer()
		s.sliceFactory.Start(runCtx.Done())

		// Bound the initial sync. Without a deadline, missing get/list/watch RBAC
		// on resourceslices makes the reflector retry indefinitely and
		// WaitForCacheSync never returns, hanging collector startup. The startup
		// ctx still aborts the wait early if the collector is shutting down. On
		// failure we tear down our own reflector and return an error; the caller
		// decides whether that is fatal (DRA-only) or a non-fatal fallback to the
		// device-plugin path.
		syncCtx, syncCancel := context.WithTimeout(ctx, cacheSyncTimeout)
		defer syncCancel()
		if !cache.WaitForCacheSync(syncCtx.Done(), s.sliceInformer.HasSynced) {
			cancel()
			return fmt.Errorf("DRA ResourceSlice informer cache did not sync within %s "+
				"(verify the ServiceAccount has get/list/watch on resourceslices)", cacheSyncTimeout)
		}
	}

	s.rebuild(runCtx)

	s.wg.Add(1)
	go s.pollLoop(runCtx)
	return nil
}

// Stop shuts down the rebuild loop and informer.
func (s *Store) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// GetDRAContainerInfo returns the pods/containers that use the given
// DRA-allocated device. A device may be used by more than one pod (a shared
// ResourceClaim or consumable-capacity sharing); all are returned so the caller
// can fan the metric out per pod. Returns nil when the device is not tracked.
func (s *Store) GetDRAContainerInfo(deviceID, driverName string) []podinfo.ContainerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	owners := s.deviceToPod[draDeviceKey{DeviceID: deviceID, DriverName: driverName}]
	if len(owners) == 0 {
		return nil
	}
	out := make([]podinfo.ContainerInfo, len(owners))
	copy(out, owners)
	return out
}

func (s *Store) pollLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.rebuild(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// rebuild recomputes the full device-to-pods map from the latest kubelet
// bindings and the ResourceSlice attribute bridge. A device used by multiple
// pods maps to all of them; the processor fans the metric out per pod.
func (s *Store) rebuild(ctx context.Context) {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()

	// The socket gives the active pod↔claim bindings; group the owning pods by
	// claim (a claim shared by several running pods yields more than one pod).
	type claimRef struct{ namespace, name string }
	claimPods := make(map[claimRef][]podinfo.ContainerInfo)
	bindings := s.source.ListDRADevices()
	for i := range bindings {
		b := &bindings[i]
		if b.ClaimName == "" {
			continue
		}
		ref := claimRef{namespace: b.ClaimNamespace, name: b.ClaimName}
		claimPods[ref] = append(claimPods[ref], b.Pod)
	}

	sliceIndex := s.buildSliceIndex()

	// Drop cached claim allocations no longer referenced by an active pod.
	for key := range s.claimCache {
		ns, name, _ := strings.Cut(key, "/")
		if _, ok := claimPods[claimRef{namespace: ns, name: name}]; !ok {
			delete(s.claimCache, key)
		}
	}

	newMap := make(map[draDeviceKey][]podinfo.ContainerInfo)

	for ref, pods := range claimPods {
		info, ok := s.getClaimInfo(ctx, ref.namespace, ref.name)
		if !ok {
			// Claim could not be read this cycle (RBAC/not found/transient); skip
			// without caching so the next rebuild retries.
			continue
		}

		for _, res := range info.results {
			dc, tracked := s.trackedDrivers[res.driver]
			if !tracked {
				continue
			}

			// Resolve the correlation key: the device name by default, or the value
			// of a ResourceSlice attribute when the device type keys on one.
			source := res.device
			if dc.sourceAttribute != "" {
				attrs, ok := sliceIndex[sliceDeviceKey{Driver: res.driver, Pool: res.pool, Device: res.device}]
				if !ok {
					s.logger.Debug("DRA device has no matching ResourceSlice, skipping attribute keying",
						zap.String("driver", res.driver),
						zap.String("pool", res.pool),
						zap.String("device", res.device),
					)
					continue
				}
				val, ok := attrs[dc.sourceAttribute]
				if !ok {
					s.logger.Debug("ResourceSlice device missing referenced attribute, skipping",
						zap.String("driver", res.driver),
						zap.String("device", res.device),
						zap.String("attribute", dc.sourceAttribute),
					)
					continue
				}
				source = val
			}

			key := draDeviceKey{DeviceID: source, DriverName: res.driver}
			for _, pod := range pods {
				newMap[key] = appendUniquePod(newMap[key], pod)
			}
		}
	}

	s.mu.Lock()
	s.deviceToPod = newMap
	s.mu.Unlock()

	s.logger.Debug("DRA store rebuilt", zap.Int("device_count", len(newMap)))
}

// appendUniquePod appends pod to owners unless an identical entry (same pod,
// namespace, and container) is already present, so a device is never fanned out
// to the same consumer twice.
func appendUniquePod(owners []podinfo.ContainerInfo, pod podinfo.ContainerInfo) []podinfo.ContainerInfo {
	for _, o := range owners {
		if o == pod {
			return owners
		}
	}
	return append(owners, pod)
}

// buildSliceIndex indexes ResourceSlice device attributes by {driver, pool,
// device}. Returns nil when no device type keys on an attribute.
func (s *Store) buildSliceIndex() map[sliceDeviceKey]map[string]string {
	if s.sliceInformer == nil {
		return nil
	}
	slices := s.sliceInformer.GetStore().List()
	index := make(map[sliceDeviceKey]map[string]string, len(slices))
	for _, obj := range slices {
		slice, ok := obj.(*resourceapi.ResourceSlice)
		if !ok {
			continue
		}
		for i := range slice.Spec.Devices {
			dev := &slice.Spec.Devices[i]
			attrs := make(map[string]string, len(dev.Attributes))
			for qn, attr := range dev.Attributes {
				if v, ok := attributeToString(attr); ok {
					attrs[string(qn)] = v
				}
			}
			index[sliceDeviceKey{Driver: slice.Spec.Driver, Pool: slice.Spec.Pool.Name, Device: dev.Name}] = attrs
		}
	}
	return index
}

// attributeToString renders a DRA device attribute as a string so it can be
// matched against a metric label regardless of the attribute's declared type.
// Different drivers model the same logical identifier differently (EFA's
// rdmaDevice is a string, Neuron's deviceId is an int), so both must bridge.
func attributeToString(attr resourceapi.DeviceAttribute) (string, bool) {
	switch {
	case attr.StringValue != nil:
		return *attr.StringValue, true
	case attr.IntValue != nil:
		return strconv.FormatInt(*attr.IntValue, 10), true
	case attr.VersionValue != nil:
		return *attr.VersionValue, true
	case attr.BoolValue != nil:
		return strconv.FormatBool(*attr.BoolValue), true
	default:
		return "", false
	}
}

// getClaimInfo returns a claim's allocation (device results), reading it from
// the API on a cache miss. The device/pool/driver triplet comes
// from the ResourceClaim rather than the kubelet socket because some DRA drivers
// (e.g. network drivers like DRANET) report the claim with empty ClaimResources
// in the Pod Resources API while the ResourceClaim's allocation is always
// populated. A claim's allocation is immutable once set, so successful reads are
// memoized; a failed read is not cached and returns ok=false so the next rebuild
// retries (rather than suppressing correlation permanently).
func (s *Store) getClaimInfo(ctx context.Context, namespace, name string) (claimInfo, bool) {
	key := namespace + "/" + name
	if ci, cached := s.claimCache[key]; cached {
		return ci, true
	}

	getCtx, cancel := context.WithTimeout(ctx, claimGetTimeout)
	defer cancel()

	claim, err := s.client.ResourceV1().ResourceClaims(namespace).Get(getCtx, name, metav1.GetOptions{})
	if err != nil {
		s.logger.Debug("Failed to get ResourceClaim; skipping this cycle",
			zap.String("namespace", namespace),
			zap.String("name", name),
			zap.Error(err),
		)
		return claimInfo{}, false
	}
	if claim.Status.Allocation == nil {
		// Allocated-but-not-yet-populated: don't cache; retry next rebuild.
		return claimInfo{}, false
	}

	var ci claimInfo
	for i := range claim.Status.Allocation.Devices.Results {
		r := &claim.Status.Allocation.Devices.Results[i]
		ci.results = append(ci.results, deviceResult{driver: r.Driver, pool: r.Pool, device: r.Device})
	}
	s.claimCache[key] = ci
	return ci, true
}
