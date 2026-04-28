// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

const (
	epsilon    = 1e-9
	iterations = 200
)

func randomInstanceSpec(r *rand.Rand) InstanceSpec {
	spec := InstanceSpec{
		VCPUs:     int64(r.IntN(128) + 1),
		MemoryGiB: r.Float64()*4095.5 + 0.5,
		GPUCount:  int64(r.IntN(17)),
	}
	return spec
}

func randomPrice(r *rand.Rand) float64 {
	return r.Float64()*99.99 + 0.01 // $0.01–$100
}

func randomPercent(r *rand.Rand) float64 {
	return r.Float64() * 150 // 0–150% (allows burst)
}

// Property 1: Weight factors sum to unity.
func TestProperty1_WeightsSumToUnity(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	for i := 0; i < iterations; i++ {
		spec := randomInstanceSpec(r)
		cpuW, memW, gpuW := ComputeWeights(spec)
		sum := cpuW + memW + gpuW
		assert.InDelta(t, 1.0, sum, epsilon, "weights should sum to 1.0 for spec %+v, got %f", spec, sum)
		assert.GreaterOrEqual(t, cpuW, 0.0, "cpu weight should be non-negative")
		assert.GreaterOrEqual(t, memW, 0.0, "mem weight should be non-negative")
		assert.GreaterOrEqual(t, gpuW, 0.0, "gpu weight should be non-negative")
	}
}

// Property 2: Resource costs sum to hourly price.
func TestProperty2_ResourceCostsSumToHourlyPrice(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		spec := randomInstanceSpec(r)
		price := randomPrice(r)
		input := NodeCostInput{
			HourlyPrice:    price,
			Spec:           spec,
			CPUUtilization: randomPercent(r),
			CPUReserved:    randomPercent(r),
			MemUtilization: randomPercent(r),
			MemReserved:    randomPercent(r),
			GPUUtilization: randomPercent(r),
			GPUReserved:    randomPercent(r),
			NodeCount:      r.IntN(100) + 1,
			ClusterFees:    ClusterFees{ClusterFee: 0.10},
		}
		nc := engine.ComputeNodeCost(input)
		sum := nc.CPUCost + nc.MemCost + nc.GPUCost
		assert.InDelta(t, price, sum, epsilon, "cpu+mem+gpu should equal hourly price %f, got %f", price, sum)
	}
}

// Property 3: Total cost equals cpu + memory + gpu (shared NOT included).
func TestProperty3_TotalCostEqualsComponents(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		input := NodeCostInput{
			HourlyPrice:    randomPrice(r),
			Spec:           randomInstanceSpec(r),
			CPUUtilization: randomPercent(r),
			CPUReserved:    randomPercent(r),
			MemUtilization: randomPercent(r),
			MemReserved:    randomPercent(r),
			GPUUtilization: randomPercent(r),
			GPUReserved:    randomPercent(r),
			NodeCount:      r.IntN(100) + 1,
			ClusterFees:    ClusterFees{ClusterFee: 0.60, ProvisionedControlPlane: 1.65},
		}
		nc := engine.ComputeNodeCost(input)
		expected := nc.CPUCost + nc.MemCost + nc.GPUCost
		assert.InDelta(t, expected, nc.TotalCost, epsilon, "total should equal cpu+mem+gpu")
		// Shared cost should NOT be in total.
		assert.Positive(t, nc.SharedCost, "shared cost should be positive")
		assert.InDelta(t, expected, nc.TotalCost, epsilon, "shared should not be in total")
	}
}

// Property 4: Used + unused equals resource cost.
func TestProperty4_UsedPlusUnusedEqualsResourceCost(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		input := NodeCostInput{
			HourlyPrice:    randomPrice(r),
			Spec:           randomInstanceSpec(r),
			CPUUtilization: randomPercent(r),
			CPUReserved:    randomPercent(r),
			MemUtilization: randomPercent(r),
			MemReserved:    randomPercent(r),
			GPUUtilization: randomPercent(r),
			GPUReserved:    randomPercent(r),
			NodeCount:      r.IntN(100) + 1,
			ClusterFees:    ClusterFees{ClusterFee: 0.10},
		}
		nc := engine.ComputeNodeCost(input)
		assert.InDelta(t, nc.CPUCost, nc.CPUUsedCost+nc.CPUUnusedCost, epsilon, "cpu used+unused should equal cpu cost")
		assert.InDelta(t, nc.MemCost, nc.MemUsedCost+nc.MemUnusedCost, epsilon, "mem used+unused should equal mem cost")
		assert.InDelta(t, nc.GPUCost, nc.GPUUsedCost+nc.GPUUnusedCost, epsilon, "gpu used+unused should equal gpu cost")
	}
}

// Property 5: Pod cost conservation — sum of pod costs equals node cost per dimension.
func TestProperty5_PodCostConservation(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		nodeCost := NodeCost{
			CPUCost:    randomPrice(r),
			MemCost:    randomPrice(r),
			GPUCost:    randomPrice(r),
			SharedCost: r.Float64() * 5,
		}
		nPods := r.IntN(20) + 1
		pods := make([]PodCostInput, nPods)
		for j := 0; j < nPods; j++ {
			pods[j] = PodCostInput{
				PodName:        "pod-" + string(rune('a'+j)),
				Namespace:      "ns",
				NodeName:       "node-1",
				CPUEffective:   r.Float64() * 100,
				MemEffective:   r.Float64() * 100,
				GPUEffective:   r.Float64() * 100,
				CPUUtilization: r.Float64() * 100,
				MemUtilization: r.Float64() * 100,
				GPUUtilization: r.Float64() * 100,
			}
		}
		podCosts := engine.ComputePodCosts(nodeCost, pods)

		cpuSum, memSum, gpuSum := 0.0, 0.0, 0.0
		for _, pc := range podCosts {
			cpuSum += pc.CPUCost
			memSum += pc.MemCost
			gpuSum += pc.GPUCost
		}
		assert.InDelta(t, nodeCost.CPUCost, cpuSum, epsilon, "sum of pod cpu costs should equal node cpu cost")
		assert.InDelta(t, nodeCost.MemCost, memSum, epsilon, "sum of pod mem costs should equal node mem cost")
		assert.InDelta(t, nodeCost.GPUCost, gpuSum, epsilon, "sum of pod gpu costs should equal node gpu cost")
	}
}

// Property 6: Container cost conservation — sum of container costs equals pod cost per dimension.
func TestProperty6_ContainerCostConservation(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		podCost := PodCost{
			CPUCost:     randomPrice(r),
			MemCost:     randomPrice(r),
			GPUCost:     randomPrice(r),
			SharedCost:  r.Float64() * 2,
			NodeCPUCost: randomPrice(r),
			NodeMemCost: randomPrice(r),
			NodeGPUCost: randomPrice(r),
		}
		nContainers := r.IntN(5) + 1
		containers := make([]ContainerCostInput, nContainers)
		for j := 0; j < nContainers; j++ {
			containers[j] = ContainerCostInput{
				ContainerName:  "c-" + string(rune('a'+j)),
				PodName:        "pod-a",
				Namespace:      "ns",
				CPUEffective:   r.Float64() * 100,
				MemEffective:   r.Float64() * 100,
				GPUEffective:   r.Float64() * 100,
				CPUUtilization: r.Float64() * 100,
				MemUtilization: r.Float64() * 100,
				GPUUtilization: r.Float64() * 100,
			}
		}
		containerCosts := engine.ComputeContainerCosts(podCost, containers)

		cpuSum, memSum, gpuSum := 0.0, 0.0, 0.0
		for _, cc := range containerCosts {
			cpuSum += cc.CPUCost
			memSum += cc.MemCost
			gpuSum += cc.GPUCost
		}
		assert.InDelta(t, podCost.CPUCost, cpuSum, epsilon, "sum of container cpu costs should equal pod cpu cost")
		assert.InDelta(t, podCost.MemCost, memSum, epsilon, "sum of container mem costs should equal pod mem cost")
		assert.InDelta(t, podCost.GPUCost, gpuSum, epsilon, "sum of container gpu costs should equal pod gpu cost")
	}
}

// Property 11: Zero effective usage distributes cost equally.
func TestProperty11_ZeroEffectiveDistributesEqually(t *testing.T) {
	engine := NewCostEngine(zap.NewNop())
	nodeCost := NodeCost{CPUCost: 10.0, MemCost: 5.0, GPUCost: 2.0, SharedCost: 1.0}
	pods := []PodCostInput{
		{PodName: "a", Namespace: "ns", CPUEffective: 0, MemEffective: 0, GPUEffective: 0},
		{PodName: "b", Namespace: "ns", CPUEffective: 0, MemEffective: 0, GPUEffective: 0},
		{PodName: "c", Namespace: "ns", CPUEffective: 0, MemEffective: 0, GPUEffective: 0},
	}
	podCosts := engine.ComputePodCosts(nodeCost, pods)
	for _, pc := range podCosts {
		assert.InDelta(t, 10.0/3, pc.CPUCost, epsilon, "each pod should get 1/3 of cpu cost")
		assert.InDelta(t, 5.0/3, pc.MemCost, epsilon, "each pod should get 1/3 of mem cost")
		assert.InDelta(t, 2.0/3, pc.GPUCost, epsilon, "each pod should get 1/3 of gpu cost")
	}
}

func TestNodeCostExample_M5Large(t *testing.T) {
	engine := NewCostEngine(zap.NewNop())
	input := NodeCostInput{
		HourlyPrice:    0.096,
		Spec:           InstanceSpec{VCPUs: 2, MemoryGiB: 8, GPUCount: 0},
		CPUReserved:    26.25,
		CPUUtilization: 1.72,
		MemReserved:    35.00,
		MemUtilization: 12.50,
		NodeCount:      2,
		ClusterFees:    ClusterFees{ClusterFee: 0.60, ProvisionedControlPlane: 0},
	}
	nc := engine.ComputeNodeCost(input)

	// Denominator = 9*2 + 1*8 = 26
	// CPU cost = 0.096 * 18/26 = 0.066461...
	assert.InDelta(t, 0.096*18.0/26.0, nc.CPUCost, 0.0001)
	// Memory cost = 0.096 * 8/26 = 0.029538...
	assert.InDelta(t, 0.096*8.0/26.0, nc.MemCost, 0.0001)
	// GPU cost = 0
	assert.InDelta(t, 0, nc.GPUCost, epsilon)
	// Total = CPU + Mem + GPU = 0.096
	assert.InDelta(t, 0.096, nc.TotalCost, 0.0001)
	// Shared = (0.60 + 0) / 2 = 0.30
	assert.InDelta(t, 0.30, nc.SharedCost, epsilon)
	// CPU split = min(max(26.25, 1.72), 100) = 26.25
	// CPU used = 26.25% * cpu_cost
	assert.InDelta(t, 0.2625*nc.CPUCost, nc.CPUUsedCost, 0.0001)
	assert.InDelta(t, nc.CPUCost-nc.CPUUsedCost, nc.CPUUnusedCost, 0.0001)
}

// Property 7: Namespace aggregation correctness.
func TestProperty7_NamespaceAggregation(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		nPods := r.IntN(20) + 2
		pods := make([]PodCost, nPods)
		namespaces := []string{"ns-a", "ns-b", "ns-c"}
		for j := 0; j < nPods; j++ {
			pods[j] = PodCost{
				PodName:     "pod-" + string(rune('a'+j)),
				Namespace:   namespaces[r.IntN(len(namespaces))],
				CPUCost:     r.Float64() * 10,
				CPUUsedCost: r.Float64() * 5,
				MemCost:     r.Float64() * 10,
				MemUsedCost: r.Float64() * 5,
				GPUCost:     r.Float64() * 10,
				GPUUsedCost: r.Float64() * 5,
				SharedCost:  r.Float64() * 2,
			}
			pods[j].CPUUnusedCost = pods[j].CPUCost - pods[j].CPUUsedCost
			pods[j].MemUnusedCost = pods[j].MemCost - pods[j].MemUsedCost
			pods[j].GPUUnusedCost = pods[j].GPUCost - pods[j].GPUUsedCost
		}
		nsCosts := engine.ComputeNamespaceCosts(pods)

		// Sum across all namespaces should equal sum across all pods.
		totalCPU, totalMem, totalGPU := 0.0, 0.0, 0.0
		for _, ns := range nsCosts {
			totalCPU += ns.CPUCost
			totalMem += ns.MemCost
			totalGPU += ns.GPUCost
		}
		podCPU, podMem, podGPU := 0.0, 0.0, 0.0
		for _, p := range pods {
			podCPU += p.CPUCost
			podMem += p.MemCost
			podGPU += p.GPUCost
		}
		assert.InDelta(t, podCPU, totalCPU, epsilon)
		assert.InDelta(t, podMem, totalMem, epsilon)
		assert.InDelta(t, podGPU, totalGPU, epsilon)
	}
}

// Property 8: Cluster aggregation correctness.
func TestProperty8_ClusterAggregation(t *testing.T) {
	r := rand.New(rand.NewPCG(42, 0))
	engine := NewCostEngine(zap.NewNop())
	for i := 0; i < iterations; i++ {
		nNodes := r.IntN(10) + 1
		nodes := make([]NodeCost, nNodes)
		for j := 0; j < nNodes; j++ {
			nodes[j] = NodeCost{
				CPUCost:     r.Float64() * 10,
				CPUUsedCost: r.Float64() * 5,
				MemCost:     r.Float64() * 10,
				MemUsedCost: r.Float64() * 5,
				GPUCost:     r.Float64() * 10,
				GPUUsedCost: r.Float64() * 5,
			}
			nodes[j].CPUUnusedCost = nodes[j].CPUCost - nodes[j].CPUUsedCost
			nodes[j].MemUnusedCost = nodes[j].MemCost - nodes[j].MemUsedCost
			nodes[j].GPUUnusedCost = nodes[j].GPUCost - nodes[j].GPUUsedCost
		}
		fees := ClusterFees{ClusterFee: 0.60, ProvisionedControlPlane: 1.65}
		cc := engine.ComputeClusterCost(nodes, fees)

		nodeCPU, nodeMem, nodeGPU := 0.0, 0.0, 0.0
		for _, n := range nodes {
			nodeCPU += n.CPUCost
			nodeMem += n.MemCost
			nodeGPU += n.GPUCost
		}
		assert.InDelta(t, nodeCPU, cc.CPUCost, epsilon)
		assert.InDelta(t, nodeMem, cc.MemCost, epsilon)
		assert.InDelta(t, nodeGPU, cc.GPUCost, epsilon)
		assert.InDelta(t, 0.60+1.65, cc.OverheadCost, epsilon)
	}
}

// Test effective usage cap at 100.
func TestEffectiveUsageCappedAt100(t *testing.T) {
	assert.Equal(t, 100.0, effectiveUsage(120, 80))
	assert.Equal(t, 100.0, effectiveUsage(80, 120))
	assert.Equal(t, 80.0, effectiveUsage(80, 50))
	assert.Equal(t, 0.0, effectiveUsage(0, 0))
}

// Test burst normalization example from the refined doc.
func TestBurstNormalizationExample(t *testing.T) {
	engine := NewCostEngine(zap.NewNop())
	nodeCost := NodeCost{
		CPUCost:    0.0665,
		SharedCost: 0.30,
	}
	pods := []PodCostInput{
		{PodName: "A", Namespace: "ns", CPUEffective: 40, CPUUtilization: 15},
		{PodName: "B", Namespace: "ns", CPUEffective: 35, CPUUtilization: 10},
		{PodName: "C", Namespace: "ns", CPUEffective: 40, CPUUtilization: 40},
	}
	// Total split_raw = 40+35+40 = 115 > 100, so normalization kicks in.
	podCosts := engine.ComputePodCosts(nodeCost, pods)

	cpuSum := 0.0
	for _, pc := range podCosts {
		cpuSum += pc.CPUCost
	}
	assert.InDelta(t, nodeCost.CPUCost, cpuSum, epsilon, "sum should equal node cpu cost after normalization")

	// Pod A: 40/115 * 0.0665 ≈ 0.0231
	assert.InDelta(t, (40.0/115.0)*0.0665, podCosts[0].CPUCost, 0.0001)
	// Pod B: 35/115 * 0.0665 ≈ 0.0202
	assert.InDelta(t, (35.0/115.0)*0.0665, podCosts[1].CPUCost, 0.0001)
	// Pod C: 40/115 * 0.0665 ≈ 0.0231
	assert.InDelta(t, (40.0/115.0)*0.0665, podCosts[2].CPUCost, 0.0001)
}

// Test computeUsedUnused edge cases.
func TestComputeUsedUnused(t *testing.T) {
	used, unused := computeUsedUnused(10.0, 5.0, 10.0)
	assert.InDelta(t, 5.0, used, epsilon)
	assert.InDelta(t, 5.0, unused, epsilon)

	// Zero effective: all unused.
	used, unused = computeUsedUnused(10.0, 0, 0)
	assert.InDelta(t, 0, used, epsilon)
	assert.InDelta(t, 10.0, unused, epsilon)

	// Zero resource cost.
	used, unused = computeUsedUnused(0, 5.0, 10.0)
	assert.InDelta(t, 0, used, epsilon)
	assert.InDelta(t, 0, unused, epsilon)
}

// Test GPU instance weight ratios.
func TestGPUInstanceWeights(t *testing.T) {
	spec := InstanceSpec{VCPUs: 8, MemoryGiB: 32, GPUCount: 4}
	cpuW, memW, gpuW := ComputeWeights(spec)
	// denom = 9*4 + 0.9*8 + 0.1*32 = 36 + 7.2 + 3.2 = 46.4
	assert.InDelta(t, 36.0/46.4, gpuW, epsilon)
	assert.InDelta(t, 7.2/46.4, cpuW, epsilon)
	assert.InDelta(t, 3.2/46.4, memW, epsilon)
	assert.InDelta(t, 1.0, cpuW+memW+gpuW, epsilon)
}
