// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockMetricsProvider struct {
	nodes      []NodeCostInput
	nodeAttrs  []NodeAttributes
	pods       map[string][]PodCostInput
	containers map[string][]ContainerCostInput
	nodeCount  int
}

func (m *mockMetricsProvider) GetNodeData() ([]NodeCostInput, []NodeAttributes) {
	return m.nodes, m.nodeAttrs
}

func (m *mockMetricsProvider) GetPodData(nodeName string) []PodCostInput {
	return m.pods[nodeName]
}

func (m *mockMetricsProvider) GetContainerData(podName, namespace string) []ContainerCostInput {
	return m.containers[namespace+"/"+podName]
}

func (m *mockMetricsProvider) GetNodeCount() int {
	return m.nodeCount
}

func TestCostAllocator_NewRequiresAllClients(t *testing.T) {
	_, err := NewCostAllocator(CostAllocatorOpts{
		Logger:         zap.NewNop(),
		PricingClient:  nil,
		InstanceClient: nil,
		ClusterClient:  nil,
	})
	assert.Error(t, err)
}

func TestCostAllocator_NewRequiresMetricsProvider(t *testing.T) {
	pc := NewPricingClient(nil, "us-east-1", 0, zap.NewNop())
	ic := NewInstanceInfoClient(nil, 0, zap.NewNop())
	cc := NewClusterInfoClient(nil, "cluster", 0, zap.NewNop())
	_, err := NewCostAllocator(CostAllocatorOpts{
		Logger:          zap.NewNop(),
		PricingClient:   pc,
		InstanceClient:  ic,
		ClusterClient:   cc,
		MetricsProvider: nil,
	})
	assert.Error(t, err)
}

func TestCostAllocator_GetMetrics_EmptyNodes(t *testing.T) {
	mp := &mockMetricsProvider{nodeCount: 0}
	pc := &PricingClient{cache: map[string]float64{"m5.large": 0.096}, stopCh: make(chan struct{})}
	ic := &InstanceInfoClient{cache: map[string]InstanceSpec{"m5.large": {VCPUs: 2, MemoryGiB: 8}}, stopCh: make(chan struct{})}
	cc := &ClusterInfoClient{fees: ClusterFees{ClusterFee: 0.10}, stopCh: make(chan struct{})}

	ca, err := NewCostAllocator(CostAllocatorOpts{
		Logger:          zap.NewNop(),
		PricingClient:   pc,
		InstanceClient:  ic,
		ClusterClient:   cc,
		MetricsProvider: mp,
		ClusterName:     "test",
	})
	require.NoError(t, err)

	metrics := ca.GetMetrics()
	assert.Nil(t, metrics, "should return nil for empty nodes")
}

func TestCostAllocator_GetMetrics_MissingPricing(t *testing.T) {
	mp := &mockMetricsProvider{
		nodes: []NodeCostInput{
			{InstanceType: "unknown.type", CPUUtilization: 50, CPUReserved: 30},
		},
		nodeAttrs: []NodeAttributes{
			{ClusterName: "test", InstanceID: "i-123", NodeName: "node-1"},
		},
		nodeCount: 1,
	}
	pc := &PricingClient{cache: map[string]float64{}, stopCh: make(chan struct{})} // empty cache
	ic := &InstanceInfoClient{cache: map[string]InstanceSpec{"unknown.type": {VCPUs: 2, MemoryGiB: 8}}, stopCh: make(chan struct{})}
	cc := &ClusterInfoClient{fees: ClusterFees{ClusterFee: 0.10}, stopCh: make(chan struct{})}

	ca, err := NewCostAllocator(CostAllocatorOpts{
		Logger:          zap.NewNop(),
		PricingClient:   pc,
		InstanceClient:  ic,
		ClusterClient:   cc,
		MetricsProvider: mp,
		ClusterName:     "test",
	})
	require.NoError(t, err)

	metrics := ca.GetMetrics()
	assert.Nil(t, metrics, "should skip node with missing pricing")
}

func TestCostAllocator_GetMetrics_FullFlow(t *testing.T) {
	mp := &mockMetricsProvider{
		nodes: []NodeCostInput{
			{InstanceType: "m5.large", CPUUtilization: 50, CPUReserved: 30, MemUtilization: 40, MemReserved: 35},
		},
		nodeAttrs: []NodeAttributes{
			{ClusterName: "test", InstanceID: "i-123", NodeName: "node-1"},
		},
		pods: map[string][]PodCostInput{
			"node-1": {
				{PodName: "pod-a", Namespace: "ns-1", NodeName: "node-1", CPUEffective: 30, MemEffective: 20, CPUUtilization: 15, MemUtilization: 10},
				{PodName: "pod-b", Namespace: "ns-1", NodeName: "node-1", CPUEffective: 20, MemEffective: 15, CPUUtilization: 10, MemUtilization: 8},
			},
		},
		containers: map[string][]ContainerCostInput{
			"ns-1/pod-a": {
				{ContainerName: "c1", PodName: "pod-a", Namespace: "ns-1", CPUEffective: 30, MemEffective: 20, CPUUtilization: 15, MemUtilization: 10},
			},
			"ns-1/pod-b": {
				{ContainerName: "c2", PodName: "pod-b", Namespace: "ns-1", CPUEffective: 20, MemEffective: 15, CPUUtilization: 10, MemUtilization: 8},
			},
		},
		nodeCount: 1,
	}
	pc := &PricingClient{cache: map[string]float64{"m5.large": 0.096}, stopCh: make(chan struct{})}
	ic := &InstanceInfoClient{cache: map[string]InstanceSpec{"m5.large": {VCPUs: 2, MemoryGiB: 8}}, stopCh: make(chan struct{})}
	cc := &ClusterInfoClient{fees: ClusterFees{ClusterFee: 0.10}, stopCh: make(chan struct{})}

	ca, err := NewCostAllocator(CostAllocatorOpts{
		Logger:          zap.NewNop(),
		PricingClient:   pc,
		InstanceClient:  ic,
		ClusterClient:   cc,
		MetricsProvider: mp,
		ClusterName:     "test",
	})
	require.NoError(t, err)

	metrics := ca.GetMetrics()
	// Should have: 1 node metrics + 1 pod metrics + 2 container metrics + 1 namespace metrics + 1 cluster metrics = 6
	assert.Len(t, metrics, 6, "should produce metrics for node, pods, containers, namespace, cluster")
}

func TestCostAllocator_Shutdown(t *testing.T) {
	pc := &PricingClient{cache: map[string]float64{}, stopCh: make(chan struct{})}
	ic := &InstanceInfoClient{cache: map[string]InstanceSpec{}, stopCh: make(chan struct{})}
	cc := &ClusterInfoClient{stopCh: make(chan struct{})}
	mp := &mockMetricsProvider{}

	ca, err := NewCostAllocator(CostAllocatorOpts{
		Logger:          zap.NewNop(),
		PricingClient:   pc,
		InstanceClient:  ic,
		ClusterClient:   cc,
		MetricsProvider: mp,
		ClusterName:     "test",
	})
	require.NoError(t, err)

	err = ca.Shutdown()
	assert.NoError(t, err)
}
