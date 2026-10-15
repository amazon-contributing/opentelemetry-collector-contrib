// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dra

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	resourceapi "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/podinfo"
)

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }
func ptrInt64(i int64) *int64 { return &i }

// fakeSource is a static DRABindingLister returning claim-level bindings.
type fakeSource struct{ bindings []podinfo.DRABinding }

func (f *fakeSource) ListDRADevices() []podinfo.DRABinding { return f.bindings }

// claimBinding is a pod↔claim binding as the kubelet pod-resources client reports
// it (no device detail — that comes from the ResourceClaim).
func claimBinding(namespace, claim, pod, container string) podinfo.DRABinding {
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
func sliceDev(driver, pool, device string, attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute) *resourceapi.ResourceSlice {
	return &resourceapi.ResourceSlice{
		ObjectMeta: metav1.ObjectMeta{Name: pool + "-" + driver},
		Spec: resourceapi.ResourceSliceSpec{
			Driver:   driver,
			Pool:     resourceapi.ResourcePool{Name: pool},
			NodeName: ptrStr("test-node"),
			Devices: []resourceapi.Device{
				{Name: device, Attributes: attrs},
			},
		},
	}
}

// allocatedClaim builds an allocated ResourceClaim. adminAccess sets the device
// result's AdminAccess flag; the store no longer inspects it, so it is used
// only to prove admin claims are not filtered.
func allocatedClaim(namespace, name, driver, pool, device string, adminAccess bool) *resourceapi.ResourceClaim {
	res := resourceapi.DeviceRequestAllocationResult{Driver: driver, Pool: pool, Device: device, Request: "req"}
	if adminAccess {
		res.AdminAccess = ptrBool(true)
	}
	return &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
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
	bindings := []podinfo.DRABinding{claimBinding("default", "ml-claim", "ml-pod", "trainer")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "ml-claim", "example.com/gpu", "node-pool", "gpu-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)

	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	require.Len(t, owners, 1)
	assert.Equal(t, "ml-pod", owners[0].PodName)
	assert.Equal(t, "default", owners[0].Namespace)
	assert.Equal(t, "trainer", owners[0].ContainerName)
}

func TestStore_UntrackedDriverIgnored(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("default", "c", "p", "ctr")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "c", "other.com/thing", "node-pool", "dev-0", false)}
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
	bindings := []podinfo.DRABinding{claimBinding("default", "efa-claim", "efa-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false)}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("dra.net", "test-node", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/rdmaDevice": {StringValue: ptrStr("rdmap0s31")},
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
	bindings := []podinfo.DRABinding{claimBinding("default", "nrn-claim", "nrn-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "nrn-claim", "neuron.aws.com", "test-node", "neuron-device-0", false)}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("neuron.aws.com", "test-node", "neuron-device-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
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
	bindings := []podinfo.DRABinding{claimBinding("default", "efa-claim", "efa-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false)}
	configs := []DeviceTypeConfig{{Name: "efa-dra", DriverNames: []string{"dra.net"}, SourceAttribute: "dra.net/rdmaDevice"}}

	s := newTestStore(t, bindings, nil, claims, configs)
	assert.Nil(t, s.GetDRAContainerInfo("rdmap0s31", "dra.net"))
	assert.Nil(t, s.GetDRAContainerInfo("pci-0000-00-1f-0", "dra.net"))
}

func TestStore_AttributeMissingAttrSkips(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("default", "efa-claim", "efa-pod", "app")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false)}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("dra.net", "test-node", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/pciAddress": {StringValue: ptrStr("0000:00:1f.0")},
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
		claimBinding("default", "shared-claim", "pod-a", "ctr"),
		claimBinding("default", "shared-claim", "pod-b", "ctr"),
	}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "shared-claim", "example.com/gpu", "node-pool", "gpu-0", false)}
	configs := []DeviceTypeConfig{{Name: "gpu", DriverNames: []string{"example.com/gpu"}}}

	s := newTestStore(t, bindings, nil, claims, configs)
	owners := s.GetDRAContainerInfo("gpu-0", "example.com/gpu")
	assert.ElementsMatch(t, []string{"pod-a", "pod-b"}, podNames(owners))
}

func TestStore_SharedDeviceAcrossClaimsFanOut(t *testing.T) {
	// Two claims allocating the same device (consumable-capacity sharing) -> both
	// owning pods are returned.
	bindings := []podinfo.DRABinding{
		claimBinding("default", "claim-a", "pod-a", "ctr"),
		claimBinding("default", "claim-b", "pod-b", "ctr"),
	}
	claims := []*resourceapi.ResourceClaim{
		allocatedClaim("default", "claim-a", "example.com/gpu", "node-pool", "gpu-0", false),
		allocatedClaim("default", "claim-b", "example.com/gpu", "node-pool", "gpu-0", false),
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
		claimBinding("default", "claim-a", "pod-a", "ctr-1"),
		claimBinding("default", "claim-a", "pod-a", "ctr-2"),
	}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "claim-a", "example.com/gpu", "node-pool", "gpu-0", false)}
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
	bindings := []podinfo.DRABinding{claimBinding("default", "monitor-claim", "monitor-pod", "agent")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "monitor-claim", "example.com/gpu", "node-pool", "gpu-0", true)}
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
		claimBinding("default", "monitor-claim", "monitor-pod", "agent"),
		claimBinding("default", "work-claim", "work-pod", "trainer"),
	}
	claims := []*resourceapi.ResourceClaim{
		allocatedClaim("default", "monitor-claim", "example.com/gpu", "node-pool", "gpu-0", true),
		allocatedClaim("default", "work-claim", "example.com/gpu", "node-pool", "gpu-0", false),
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
	bindings := []podinfo.DRABinding{claimBinding("default", "missing-claim", "ml-pod", "trainer")}
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
		claimBinding("default", "efa-claim", "efa-pod", "app"),
		claimBinding("default", "nrn-claim", "nrn-pod", "app"),
	}
	claims := []*resourceapi.ResourceClaim{
		allocatedClaim("default", "efa-claim", "dra.net", "test-node", "pci-0000-00-1f-0", false),
		allocatedClaim("default", "nrn-claim", "neuron.aws.com", "test-node", "neuron-device-0", false),
	}
	slices := []*resourceapi.ResourceSlice{
		sliceDev("dra.net", "test-node", "pci-0000-00-1f-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"dra.net/rdmaDevice": {StringValue: ptrStr("rdmap0s31")},
		}),
		sliceDev("neuron.aws.com", "test-node", "neuron-device-0", map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
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
	v, ok := attributeToString(resourceapi.DeviceAttribute{StringValue: ptrStr("rdmap0s31")})
	assert.True(t, ok)
	assert.Equal(t, "rdmap0s31", v)

	v, ok = attributeToString(resourceapi.DeviceAttribute{IntValue: ptrInt64(7)})
	assert.True(t, ok)
	assert.Equal(t, "7", v)

	v, ok = attributeToString(resourceapi.DeviceAttribute{VersionValue: ptrStr("1.2.0")})
	assert.True(t, ok)
	assert.Equal(t, "1.2.0", v)

	v, ok = attributeToString(resourceapi.DeviceAttribute{BoolValue: ptrBool(true)})
	assert.True(t, ok)
	assert.Equal(t, "true", v)

	_, ok = attributeToString(resourceapi.DeviceAttribute{})
	assert.False(t, ok)
}

// --- cache pruning ---

func TestStore_ClaimCachePruned(t *testing.T) {
	bindings := []podinfo.DRABinding{claimBinding("default", "claim-a", "pod-a", "ctr")}
	claims := []*resourceapi.ResourceClaim{allocatedClaim("default", "claim-a", "example.com/gpu", "node-pool", "gpu-0", false)}
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
