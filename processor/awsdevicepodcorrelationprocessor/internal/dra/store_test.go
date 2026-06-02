// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dra

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/types"
)

func ptrStr(s string) *string { return &s }

func newTestStore(t *testing.T, pods []*corev1.Pod, claims []*resourceapi.ResourceClaim, configs []DeviceTypeConfig) *Store {
	t.Helper()

	objects := make([]runtime.Object, 0, len(pods)+len(claims))
	for _, p := range pods {
		objects = append(objects, p)
	}
	for _, c := range claims {
		objects = append(objects, c)
	}

	client := fakeclient.NewSimpleClientset(objects...)

	// Build trackedDrivers (normally done in Start()).
	trackedDrivers := make(map[string]driverConfig)
	for _, cfg := range configs {
		for _, dn := range cfg.DriverNames {
			trackedDrivers[dn] = driverConfig{pattern: cfg.DeviceIDPattern}
		}
	}

	s := &Store{
		deviceToPod:    make(map[draDeviceKey]types.ContainerInfo),
		configs:        configs,
		trackedDrivers: trackedDrivers,
		nodeName:       "test-node",
		logger:         zaptest.NewLogger(t),
		client:         client,
	}

	// Create informers and populate their stores directly.
	factory := informers.NewSharedInformerFactory(client, 0)
	podInformer := factory.Core().V1().Pods().Informer()
	claimInformer := factory.Resource().V1beta1().ResourceClaims().Informer()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	factory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced, claimInformer.HasSynced)

	s.podInformer = podInformer
	s.claimInformer = claimInformer
	s.cancel = cancel

	s.rebuild()
	return s
}

// --- Basic Correlation Tests ---

func TestStore_BasicGPUCorrelation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ml-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu-claim", ResourceClaimName: ptrStr("ml-pod-gpu")},
			},
			Containers: []corev1.Container{
				{
					Name: "trainer",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpu-claim"}},
					},
				},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ml-pod-gpu", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "node-pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{
			Name:            "gpu",
			DriverNames:     []string{"gpu.nvidia.com"},
			DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`),
		},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "ml-pod", info.PodName)
	assert.Equal(t, "default", info.Namespace)
	assert.Equal(t, "trainer", info.ContainerName)
}

func TestStore_NoPatternUsesFullDeviceName(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "neuron-pod", Namespace: "ml"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "neuron-claim", ResourceClaimName: ptrStr("neuron-pod-dev")},
			},
			Containers: []corev1.Container{
				{
					Name: "worker",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "neuron-claim"}},
					},
				},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "neuron-pod-dev", Namespace: "ml"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "neuron.aws.com", Pool: "pool", Device: "neuron-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "neuron", DriverNames: []string{"neuron.aws.com"}},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("neuron-0", "neuron.aws.com")
	require.NotNil(t, info)
	assert.Equal(t, "neuron-pod", info.PodName)
	assert.Equal(t, "worker", info.ContainerName)

	// Without pattern, extracted ID "0" should not match.
	info = s.GetDRAContainerInfo("0", "neuron.aws.com")
	assert.Nil(t, info)
}

func TestStore_MultipleDevicesInOneClaim(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-gpu", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpus", ResourceClaimName: ptrStr("multi-gpu-claim")},
			},
			Containers: []corev1.Container{
				{
					Name: "train",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpus"}},
					},
				},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-gpu-claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-1", Request: "req"},
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-2", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{
			Name:            "gpu",
			DriverNames:     []string{"gpu.nvidia.com"},
			DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`),
		},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	for _, id := range []string{"0", "1", "2"} {
		info := s.GetDRAContainerInfo(id, "gpu.nvidia.com")
		require.NotNil(t, info, "should find device %s", id)
		assert.Equal(t, "multi-gpu", info.PodName)
		assert.Equal(t, "train", info.ContainerName)
	}

	info := s.GetDRAContainerInfo("3", "gpu.nvidia.com")
	assert.Nil(t, info)
}

// --- Edge Cases ---

func TestStore_UntrackedDriverIgnored(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "claim", ResourceClaimName: ptrStr("my-claim")},
			},
			Containers: []corev1.Container{
				{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "claim"}},
					},
				},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "my-claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "some.other.driver", Pool: "pool", Device: "dev-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)
	info := s.GetDRAContainerInfo("dev-0", "some.other.driver")
	assert.Nil(t, info)
}

func TestStore_ClaimNotAllocated(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu-claim", ResourceClaimName: ptrStr("pending-claim")},
			},
			Containers: []corev1.Container{
				{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpu-claim"}},
					},
				},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pending-claim", Namespace: "default"},
		Status:     resourceapi.ResourceClaimStatus{},
	}

	configs := []DeviceTypeConfig{
		{
			Name:            "gpu",
			DriverNames:     []string{"gpu.nvidia.com"},
			DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`),
		},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)
	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	assert.Nil(t, info)
}

func TestStore_ClaimMissing(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "orphan-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu-claim", ResourceClaimName: ptrStr("nonexistent-claim")},
			},
			Containers: []corev1.Container{
				{
					Name: "c",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpu-claim"}},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{
			Name:            "gpu",
			DriverNames:     []string{"gpu.nvidia.com"},
			DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`),
		},
	}

	// No claims exist at all.
	s := newTestStore(t, []*corev1.Pod{pod}, nil, configs)
	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	assert.Nil(t, info)
}

func TestStore_TemplatedClaimName(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimTemplateName: ptrStr("gpu-template")},
			},
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpu"}},
					},
				},
			},
		},
	}

	// Generated name: <pod-name>-<claim-name>
	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl-pod-gpu", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{
			Name:            "gpu",
			DriverNames:     []string{"gpu.nvidia.com"},
			DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`),
		},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "tmpl-pod", info.PodName)
	assert.Equal(t, "app", info.ContainerName)
}

// --- Multi-container tests ---

func TestStore_MultipleContainersDifferentClaims(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-c", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu-claim", ResourceClaimName: ptrStr("multi-c-gpu")},
				{Name: "neuron-claim", ResourceClaimName: ptrStr("multi-c-neuron")},
			},
			Containers: []corev1.Container{
				{
					Name: "gpu-worker",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpu-claim"}},
					},
				},
				{
					Name: "neuron-worker",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "neuron-claim"}},
					},
				},
			},
		},
	}

	gpuClaim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-c-gpu", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	neuronClaim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-c-neuron", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "neuron.aws.com", Pool: "pool", Device: "neuron-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
		{Name: "neuron", DriverNames: []string{"neuron.aws.com"}, DeviceIDPattern: regexp.MustCompile(`neuron-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{gpuClaim, neuronClaim}, configs)

	gpuInfo := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, gpuInfo)
	assert.Equal(t, "gpu-worker", gpuInfo.ContainerName)

	neuronInfo := s.GetDRAContainerInfo("0", "neuron.aws.com")
	require.NotNil(t, neuronInfo)
	assert.Equal(t, "neuron-worker", neuronInfo.ContainerName)
}

func TestStore_SharedClaimBetweenContainers_FirstWins(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "shared-gpu", ResourceClaimName: ptrStr("shared-gpu-claim")},
			},
			Containers: []corev1.Container{
				{
					Name: "first-container",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "shared-gpu"}},
					},
				},
				{
					Name: "second-container",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "shared-gpu"}},
					},
				},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-gpu-claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "first-container", info.ContainerName, "first container should win for shared claims")
}

func TestStore_ClaimReferencedByPodButNoContainerClaims(t *testing.T) {
	// Pod references a claim at pod-level but no container declares it in resources.claims.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "no-container-ref", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu-claim", ResourceClaimName: ptrStr("orphan-claim")},
			},
			Containers: []corev1.Container{
				{Name: "app"},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "orphan-claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "no-container-ref", info.PodName)
	assert.Equal(t, "", info.ContainerName, "empty container name when no container references the claim")
}

func TestStore_InitContainerClaim(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "init-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu-claim", ResourceClaimName: ptrStr("init-gpu-claim")},
			},
			InitContainers: []corev1.Container{
				{
					Name: "init-worker",
					Resources: corev1.ResourceRequirements{
						Claims: []corev1.ResourceClaim{{Name: "gpu-claim"}},
					},
				},
			},
			Containers: []corev1.Container{
				{Name: "main-app"},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "init-gpu-claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "init-worker", info.ContainerName, "init container should be found if no regular container claims it")
}

// --- Multi-pod / namespace tests ---

func TestStore_MultiplePodsMultipleNamespaces(t *testing.T) {
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "ns1"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimName: ptrStr("claim-a")},
			},
			Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "gpu"}}}},
			},
		},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-b", Namespace: "ns2"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimName: ptrStr("claim-b")},
			},
			Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "gpu"}}}},
			},
		},
	}

	claim1 := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-a", Namespace: "ns1"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}
	claim2 := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-b", Namespace: "ns2"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-1", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod1, pod2}, []*resourceapi.ResourceClaim{claim1, claim2}, configs)

	info0 := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info0)
	assert.Equal(t, "pod-a", info0.PodName)
	assert.Equal(t, "ns1", info0.Namespace)

	info1 := s.GetDRAContainerInfo("1", "gpu.nvidia.com")
	require.NotNil(t, info1)
	assert.Equal(t, "pod-b", info1.PodName)
	assert.Equal(t, "ns2", info1.Namespace)
}

// --- Regex edge cases ---

func TestStore_PatternNoMatch_UsesFullName(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "claim", ResourceClaimName: ptrStr("claim")},
			},
			Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "claim"}}}},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "unexpected-format", Request: "req"},
					},
				},
			},
		},
	}

	// Pattern expects "gpu-<digits>" but device is "unexpected-format".
	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	// Should fall back to full name since regex didn't match.
	info := s.GetDRAContainerInfo("unexpected-format", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "pod", info.PodName)

	// Partial extraction should not work.
	info = s.GetDRAContainerInfo("format", "gpu.nvidia.com")
	assert.Nil(t, info)
}

func TestStore_PatternMultipleCaptureGroups(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "claim", ResourceClaimName: ptrStr("claim")},
			},
			Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "claim"}}}},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-3-mig-2", Request: "req"},
					},
				},
			},
		},
	}

	// Only first capture group is used.
	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)-mig-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("3", "gpu.nvidia.com")
	require.NotNil(t, info)

	// Second capture group value should not match.
	info = s.GetDRAContainerInfo("2", "gpu.nvidia.com")
	assert.Nil(t, info)
}

// --- Debounce tests ---

func TestStore_DebounceCoalescesRapidEvents(t *testing.T) {
	s := &Store{
		deviceToPod:    make(map[draDeviceKey]types.ContainerInfo),
		trackedDrivers: make(map[string]driverConfig),
		logger:         zap.NewNop(),
	}

	// Set up minimal informers with empty stores.
	objects := []runtime.Object{}
	client := fakeclient.NewSimpleClientset(objects...)
	factory := informers.NewSharedInformerFactory(client, 0)
	podInformer := factory.Core().V1().Pods().Informer()
	claimInformer := factory.Resource().V1beta1().ResourceClaims().Informer()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	factory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced, claimInformer.HasSynced)

	s.podInformer = podInformer
	s.claimInformer = claimInformer

	// Fire many events rapidly.
	for range 20 {
		s.scheduleRebuild()
	}

	// Wait for debounce to fire.
	time.Sleep(debounceInterval + 100*time.Millisecond)

	// Timer should be cleared.
	s.debounceMu.Lock()
	assert.Nil(t, s.debounceTimer)
	s.debounceMu.Unlock()
}

// --- Concurrency test ---

func TestStore_ConcurrentGetDuringRebuild(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimName: ptrStr("claim")},
			},
			Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "gpu"}}}},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	configs := []DeviceTypeConfig{
		{Name: "gpu", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`)},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			s.rebuild()
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			s.GetDRAContainerInfo("0", "gpu.nvidia.com")
		}
	}()
	wg.Wait()
}

// --- Lifecycle tests ---

func TestStore_MissingNodeNameError(t *testing.T) {
	s := NewStore(zap.NewNop())
	s.nodeName = ""
	err := s.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node name is required")
}

func TestStore_StopWithNoStart(t *testing.T) {
	s := NewStore(zap.NewNop(), WithNodeName("node"))
	// Should not panic.
	s.Stop()
}

func TestStore_NodeNameFromEnv(t *testing.T) {
	t.Setenv("NODE_NAME", "env-node")
	s := NewStore(zap.NewNop())
	assert.Equal(t, "env-node", s.nodeName)
}

func TestStore_NodeNameOptionOverridesEnv(t *testing.T) {
	t.Setenv("NODE_NAME", "env-node")
	s := NewStore(zap.NewNop(), WithNodeName("option-node"))
	assert.Equal(t, "option-node", s.nodeName)
}

// --- Multiple drivers per config test ---

func TestStore_MultipleDriverNames(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
			ResourceClaims: []corev1.PodResourceClaim{
				{Name: "gpu", ResourceClaimName: ptrStr("claim")},
			},
			Containers: []corev1.Container{
				{Name: "c", Resources: corev1.ResourceRequirements{Claims: []corev1.ResourceClaim{{Name: "gpu"}}}},
			},
		},
	}

	claim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "default"},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{
						{Driver: "gpu.nvidia.com", Pool: "pool", Device: "gpu-0", Request: "req"},
					},
				},
			},
		},
	}

	// Config with multiple driver names — both should match.
	configs := []DeviceTypeConfig{
		{
			Name:            "gpu",
			DriverNames:     []string{"gpu.nvidia.com", "gpu.amd.com"},
			DeviceIDPattern: regexp.MustCompile(`gpu-(\d+)`),
		},
	}

	s := newTestStore(t, []*corev1.Pod{pod}, []*resourceapi.ResourceClaim{claim}, configs)

	info := s.GetDRAContainerInfo("0", "gpu.nvidia.com")
	require.NotNil(t, info)
	assert.Equal(t, "pod", info.PodName)

	// AMD driver not in claim, so no match.
	info = s.GetDRAContainerInfo("0", "gpu.amd.com")
	assert.Nil(t, info)
}
