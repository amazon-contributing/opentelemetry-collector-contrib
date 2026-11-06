// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dra

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.uber.org/zap/zaptest"
	authorizationv1 "k8s.io/api/authorization/v1"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"
)

//go:fix inline
func ptrInt64(i int64) *int64 { return new(i) }

// fakeSource is a static DRABindingLister returning claim-level bindings.
type fakeSource struct{ bindings []podinfo.DRABinding }

func (f *fakeSource) ListDRADevices() []podinfo.DRABinding { return f.bindings }

// claimBinding is a pod↔claim binding as the kubelet pod-resources client reports
// it (no device detail — that comes from the ResourceClaim).
func claimBinding(claim, pod, container string) podinfo.DRABinding {
	const namespace = "default"
	return podinfo.DRABinding{
		ClaimName:      claim,
		ClaimNamespace: namespace,
		Pod: podinfo.ContainerInfo{
			PodName:       pod,
			Namespace:     namespace,
			ContainerName: container,
		},
	}
}

// sliceDev builds a single-device ResourceSlice with the given attributes.
func sliceDev(driver, device string, attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute) *resourceapi.ResourceSlice {
	const pool = "test-node"
	return &resourceapi.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: pool + "-" + driver},
		Spec: resourceapi.ResourceSliceSpec{
			Driver:   driver,
			Pool:     resourceapi.ResourcePool{Name: pool},
			NodeName: new("test-node"),
			Devices: []resourceapi.Device{
				{Name: device, Attributes: attrs},
			},
		},
	}
}

// allocatedClaim builds an allocated ResourceClaim. adminAccess sets the device
// result's AdminAccess flag; the store no longer inspects it, so it is used
// only to prove admin claims are not filtered.
func allocatedClaim(name, driver, pool, device string, adminAccess bool) *resourceapi.ResourceClaim {
	const namespace = "default"
	res := resourceapi.DeviceRequestAllocationResult{Driver: driver, Pool: pool, Device: device, Request: "req"}
	if adminAccess {
		res.AdminAccess = new(true)
	}
	return &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			// A Pod owner marks this as generated from a ResourceClaimTemplate
			// (immutable), so the store memoizes it. Tests needing a named claim
			// clear OwnerReferences.
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: name + "-pod"}},
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{res},
				},
			},
		},
	}
}

func podNames(owners []podinfo.ContainerInfo) []string {
	out := make([]string, len(owners))
	for i, o := range owners {
		out[i] = o.PodName
	}
	return out
}

func newTestStore(t *testing.T, bindings []podinfo.DRABinding, slices []*resourceapi.ResourceSlice, claims []*resourceapi.ResourceClaim, configs []DeviceTypeConfig) *Store {
	t.Helper()

	objects := make([]runtime.Object, 0, len(slices)+len(claims))
	for _, sl := range slices {
		objects = append(objects, sl)
	}
	for _, c := range claims {
		objects = append(objects, c)
	}
	client := fakeclient.NewSimpleClientset(objects...)

	trackedDrivers := make(map[string]driverConfig)
	needSlices := false
	for _, cfg := range configs {
		for _, dn := range cfg.DriverNames {
			trackedDrivers[dn] = driverConfig{sourceAttribute: cfg.SourceAttribute}
		}
		if cfg.SourceAttribute != "" {
			needSlices = true
		}
	}

	s := &Store{
		deviceToPod:     make(map[draDeviceKey][]podinfo.ContainerInfo),
		claimCache:      make(map[string]claimInfo),
		configs:         configs,
		trackedDrivers:  trackedDrivers,
		needSlices:      needSlices,
		nodeName:        "test-node",
		logger:          zaptest.NewLogger(t),
		client:          client,
		source:          &fakeSource{bindings: bindings},
		refreshInterval: defaultRefreshInterval,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	if needSlices {
		factory := informers.NewSharedInformerFactory(client, 0)
		sliceInformer := factory.Resource().V1().ResourceSlices().Informer()
		factory.Start(ctx.Done())
		cache.WaitForCacheSync(ctx.Done(), sliceInformer.HasSynced)
		s.sliceInformer = sliceInformer
	}

	s.rebuild(ctx)
	return s
}

// --- Basic correlation ---

func TestStore_DeviceNameKeying(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("ml-claim", "ml-pod", "trainer")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("ml-claim", "example.com/gpu", "node-pool", "gpu-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)

	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	require.Len(t, owners, 1)
	assert.Equal(t, "ml-pod", owners[0].PodName)
	assert.Equal(t, "default", owners[0].Namespace)
	assert.Equal(t, "trainer", owners[0].ContainerName)
}

func TestStore_UntrackedDriverIgnored(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("c", "p", "ctr")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("c", "other.com/thing", "node-pool", "dev-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	assert.Nil(t, s.GetDRAContainerInfo("dev-0", "other.com/thing"))
}

func TestStore_NoData(t *testing.T) {
	s := newTestStore(t, nil, nil, nil, []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}})
	assert.Nil(t, s.GetDRAContainerInfo("gpu-0", "example.com/gpu"))
}

// --- Attribute bridge (string value: EFA via dra.net) ---

func TestStore_AttributeKeyingString(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("efa-claim", "efa-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false)}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("dra.net", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/rdmaDevice": {StringValue: new("rdmap0s31")},
		}),
	}
	configs := []DeviceTypeConfig{{Name: "efa-dra", DriverNames: []string{"dra.net"}, SourceAttribute: "dra.net/rdmaDevice"}}

	s := newTestStore(t, bindings, slices, claims, configs)

	owners := s.GetDRAContainerInfo("rdmap0s31", "dra.net")
	require.Len(t, owners, 1, "device must be keyed on the attribute value")
	assert.Equal(t, "efa-pod", owners[0].PodName)
	assert.Nil(t, s.GetDRAContainerInfo("pci-0000-00-1f-0", "dra.net"))
}

// --- Attribute bridge (int value: Neuron deviceId) ---

func TestStore_AttributeKeyingInt(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("nrn-claim", "nrn-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("nrn-claim", "neuron.aws.com", "test-node", "neuron-device-0", false)}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("neuron.aws.com", "neuron-device-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"deviceId": {IntValue: ptrInt64(0)},
		}),
	}
	configs := []DeviceTypeConfig{{Name: "neuron-dra", DriverNames: []string{"neuron.aws.com"}, SourceAttribute: "deviceId"}}

	s := newTestStore(t, bindings, slices, claims, configs)

	owners := s.GetDRAContainerInfo("0", "neuron.aws.com")
	require.Len(t, owners, 1, "int attribute must be stringified and keyed as \"0\"")
	assert.Equal(t, "nrn-pod", owners[0].PodName)
}

func TestStore_AttributeMissingSliceSkips(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("efa-claim", "efa-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false)}
	configs := []DeviceTypeConfig{{Name: "efa-dra", DriverNames: []string{"dra.net"}, SourceAttribute: "dra.net/rdmaDevice"}}

	s := newTestStore(t, bindings, nil, claims, configs)
	assert.Nil(t, s.GetDRAContainerInfo("rdmap0s31", "dra.net"))
	assert.Nil(t, s.GetDRAContainerInfo("pci-0000-00-1f-0", "dra.net"))
}

func TestStore_AttributeMissingAttrSkips(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("efa-claim", "efa-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false)}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("dra.net", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/pciAddress": {StringValue: new("0000:00:1f.0")},
		}),
	}
	configs := []DeviceTypeConfig{{Name: "efa-dra", DriverNames: []string{"dra.net"}, SourceAttribute: "dra.net/rdmaDevice"}}

	s := newTestStore(t, bindings, slices, claims, configs)
	assert.Nil(t, s.GetDRAContainerInfo("rdmap0s31", "dra.net"))
}

// --- Shared devices (fan-out to all consumers) ---

func TestStore_SharedClaimFanOut(t *testing.T) {
	// One claim reserved by two distinct pods (shared) -> both pods are returned
	// so the processor can emit a series for each.
	bindings := []podinfo.DRABinding{
		claimBinding("shared-claim", "pod-a", "ctr"),
		claimBinding("shared-claim", "pod-b", "ctr"),
	}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("shared-claim", "example.com/gpu", "node-pool", "gpu-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	assert.ElementsMatch(t, []string{"pod-a", "pod-b"}, podNames(owners))
}

func TestStore_SharedDeviceAcrossClaimsFanOut(t *testing.T) {
	// Two claims allocating the same device (consumable-capacity sharing) -> both
	// owning pods are returned.
	bindings := []podinfo.DRABinding{
		claimBinding("claim-a", "pod-a", "ctr"),
		claimBinding("claim-b", "pod-b", "ctr"),
	}
	claims := []*resourceapi.ResourceClaim{
		allocatedClaim("claim-a", "example.com/gpu", "node-pool", "gpu-0", false),
		allocatedClaim("claim-b", "example.com/gpu", "node-pool", "gpu-0", false),
	}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	assert.ElementsMatch(t, []string{"pod-a", "pod-b"}, podNames(owners))
}

func TestStore_SamePodTwoContainers(t *testing.T) {
	// Two containers of one pod referencing the same claim -> one owner per
	// pod+namespace+container.
	bindings := []podinfo.DRABinding{
		claimBinding("claim-a", "pod-a", "ctr-1"),
		claimBinding("claim-a", "pod-a", "ctr-2"),
	}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("claim-a", "example.com/gpu", "node-pool", "gpu-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	require.Len(t, owners, 2)
	containers := map[string]struct{}{}
	for _, o := range owners {
		assert.Equal(t, "pod-a", o.PodName)
		containers[o.ContainerName] = struct{}{}
	}
	assert.Len(t, containers, 2, "expected a distinct owner per container")
}

// --- adminAccess is no longer filtered ---

func TestStore_AdminAccessIncludedInFanOut(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("monitor-claim", "monitor-pod", "agent")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("monitor-claim", "example.com/gpu", "node-pool", "gpu-0", true)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	require.Len(t, owners, 1, "adminAccess claims are no longer filtered")
	assert.Equal(t, "monitor-pod", owners[0].PodName)
}

func TestStore_AdminPlusWorkloadBothReturned(t *testing.T) {
	// A monitoring pod (adminAccess) and a workload pod both use gpu-0 via separate
	// claims. Both are returned (the processor fans out to each); no drop.
	bindings := []podinfo.DRABinding{
		claimBinding("monitor-claim", "monitor-pod", "agent"),
		claimBinding("work-claim", "work-pod", "trainer"),
	}
	claims := []*resourceapi.ResourceClaim{
		allocatedClaim("monitor-claim", "example.com/gpu", "node-pool", "gpu-0", true),
		allocatedClaim("work-claim", "example.com/gpu", "node-pool", "gpu-0", false),
	}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	assert.ElementsMatch(t, []string{"monitor-pod", "work-pod"}, podNames(owners))
}

func TestStore_ClaimUnreadableNotCorrelated(t *testing.T) {
	// The socket reports a claim, but no ResourceClaim object exists, so its device
	// detail cannot be read. The device is simply not correlated this cycle (and
	// the verdict is not cached, so a later rebuild retries).
	bindings := []podinfo.DRABinding{claimBinding("missing-claim", "ml-pod", "trainer")}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, nil, configs)
	assert.Nil(t, s.GetDRAContainerInfo("gpu-0", "example.com/gpu"))
	s.rebuildMu.Lock()
	_, cached := s.claimCache["default/missing-claim"]
	s.rebuildMu.Unlock()
	assert.False(t, cached, "a failed claim read must not be cached")
}

// --- Multiple drivers / device types ---

func TestStore_MultipleDeviceTypes(t *testing.T) {
	bindings := []podinfo.DRABinding{
		claimBinding("efa-claim", "efa-pod", "app"),
		claimBinding("nrn-claim", "nrn-pod", "app"),
	}
	claims := []*resourceapi.ResourceClaim{
		allocatedClaim("efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false),
		allocatedClaim("nrn-claim", "neuron.aws.com", "test-node", "neuron-device-0", false),
	}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("dra.net", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/rdmaDevice": {StringValue: new("rdmap0s31")},
		}),
		sliceDev("neuron.aws.com", "neuron-device-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"deviceId": {IntValue: ptrInt64(0)},
		}),
	}
	configs := []DeviceTypeConfig{
		{Name: "efa-dra", DriverNames: []string{"dra.net"}, SourceAttribute: "dra.net/rdmaDevice"},
		{Name: "neuron-dra", DriverNames: []string{"neuron.aws.com"}, SourceAttribute: "deviceId"},
	}

	s := newTestStore(t, bindings, slices, claims, configs)

	efa := s.GetDRAContainerInfo("rdmap0s31", "dra.net")
	require.Len(t, efa, 1)
	assert.Equal(t, "efa-pod", efa[0].PodName)

	nrn := s.GetDRAContainerInfo("0", "neuron.aws.com")
	require.Len(t, nrn, 1)
	assert.Equal(t, "nrn-pod", nrn[0].PodName)
}

// --- attributeToString ---

func TestAttributeToString(t *testing.T) {
	v, ok := attributeToString(resourceapi.DeviceAttribute{StringValue: new("rdmap0s31")})
	assert.True(t, ok)
	assert.Equal(t, "rdmap0s31", v)

	v, ok = attributeToString(resourceapi.DeviceAttribute{IntValue: ptrInt64(7)})
	assert.True(t, ok)
	assert.Equal(t, "7", v)

	v, ok = attributeToString(resourceapi.DeviceAttribute{VersionValue: new("1.2.0")})
	assert.True(t, ok)
	assert.Equal(t, "1.2.0", v)

	v, ok = attributeToString(resourceapi.DeviceAttribute{BoolValue: new(true)})
	assert.True(t, ok)
	assert.Equal(t, "true", v)

	_, ok = attributeToString(resourceapi.DeviceAttribute{})
	assert.False(t, ok)
}

// --- cache pruning ---

func TestStore_ClaimCachePruned(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("claim-a", "pod-a", "ctr")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("claim-a", "example.com/gpu", "node-pool", "gpu-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	s.rebuildMu.Lock()
	_, cached := s.claimCache["default/claim-a"]
	s.rebuildMu.Unlock()
	assert.True(t, cached, "claim allocation should be cached after a rebuild")

	// Binding goes away; the cache entry must be pruned on the next rebuild.
	s.source = &fakeSource{bindings: nil}
	s.rebuild(t.Context())
	s.rebuildMu.Lock()
	_, cached = s.claimCache["default/claim-a"]
	s.rebuildMu.Unlock()
	assert.False(t, cached, "stale claim allocation should be pruned")
}

// --- named (non-templated) claims are re-read, not cached ---

func TestStore_NamedClaimNotCached(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("named-claim", "pod-a", "ctr")}
	claim := allocatedClaim("named-claim", "example.com/gpu", "node-pool", "gpu-0", false)
	claim.OwnerReferences = nil // directly referenced (named) claim: reallocatable
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, []*resourceapi.ResourceClaim{claim}, configs)

	s.rebuildMu.Lock()
	_, cached := s.claimCache["default/named-claim"]
	s.rebuildMu.Unlock()
	assert.False(t, cached, "a named (non-templated) claim must not be cached so reallocation is picked up")

	// Correlation still works from the fresh read.
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	require.Len(t, owners, 1)
	assert.Equal(t, "pod-a", owners[0].PodName)
}

// --- resourceclaims get permission preflight ---

func TestStore_EnsureResourceClaimGetAllowed(t *testing.T) {
	t.Run("allowed", func(t *testing.T) {
		c := fakeclient.NewSimpleClientset()
		c.PrependReactor("create", "selfsubjectaccessreviews", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, &authorizationv1.SelfSubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
			}, nil
		})
		s := &Store{logger: zaptest.NewLogger(t), client: c}
		assert.NoError(t, s.ensureResourceClaimGetAllowed(t.Context()))
	})

	t.Run("denied", func(t *testing.T) {
		c := fakeclient.NewSimpleClientset()
		c.PrependReactor("create", "selfsubjectaccessreviews", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, &authorizationv1.SelfSubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false, Reason: "no access"},
			}, nil
		})
		s := &Store{logger: zaptest.NewLogger(t), client: c}
		assert.Error(t, s.ensureResourceClaimGetAllowed(t.Context()))
	})
}

// --- Start/Stop owns informer lifetime (no goroutine leak) ---

func TestStore_StartStopNoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	client := fakeclient.NewSimpleClientset(
		sliceDev("dra.net", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/rdmaDevice": {StringValue: new("rdmap0s31")},
		}),
	)
	client.PrependReactor("create", "selfsubjectaccessreviews", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
		}, nil
	})

	s := NewStore(zaptest.NewLogger(t),
		WithBindingSource(&fakeSource{}),
		WithConfigs([]DeviceTypeConfig{{Name: "efa-dra", DriverNames: []string{"dra.net"}, SourceAttribute: "dra.net/rdmaDevice"}}),
		WithKubeClient(client),
		WithNodeName("test-node"),
	)
	require.NoError(t, s.Start(t.Context()))
	s.Stop() // must wait for the ResourceSlice informer goroutines to exit
}

// --- bare vs driver-qualified attribute names are the same attribute ---

func TestStore_AttributeQualifiedNameNormalization(t *testing.T) {
	const driver = "neuron.aws.com"
	for _, tc := range []struct {
		name, sliceKey, configKey string
	}{
		{"bare slice, bare config", "deviceId", "deviceId"},
		{"bare slice, prefixed config", "deviceId", "neuron.aws.com/deviceId"},
		{"prefixed slice, bare config", "neuron.aws.com/deviceId", "deviceId"},
		{"prefixed slice, prefixed config", "neuron.aws.com/deviceId", "neuron.aws.com/deviceId"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := []podinfo.DRABinding{claimBinding("nrn-claim", "nrn-pod", "app")}
			claims := []*resourceapi.ResourceClaim{allocatedClaim("nrn-claim", driver, "test-node", "neuron-device-0", false)}
			slices := []*resourceapi.ResourceSlice{
				sliceDev(driver, "neuron-device-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					resourceapi.QualifiedName(tc.sliceKey): {StringValue: new("nrn0")},
				}),
			}
			configs := []DeviceTypeConfig{{Name: "neuron-dra", DriverNames: []string{driver}, SourceAttribute: tc.configKey}}
			s := newTestStore(t, bindings, slices, claims, configs)
			owners := s.GetDRAContainerInfo("nrn0", driver)
			require.Len(t, owners, 1, "bare and driver-qualified attribute names must resolve to the same attribute")
			assert.Equal(t, "nrn-pod", owners[0].PodName)
		})
	}
}
