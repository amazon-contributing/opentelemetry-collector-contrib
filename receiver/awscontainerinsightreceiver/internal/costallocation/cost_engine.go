// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"math"

	"go.uber.org/zap"
)

// CostEngine performs pure cost computation with no AWS API calls.
type CostEngine struct {
	logger *zap.Logger
}

// NewCostEngine creates a new CostEngine.
func NewCostEngine(logger *zap.Logger) *CostEngine {
	return &CostEngine{logger: logger}
}

// ComputeWeights returns (cpuWeight, memWeight, gpuWeight) for the given instance spec.
// Standard: cpu=9*vCPU/(9*vCPU+1*mem), mem=1*mem/(9*vCPU+1*mem), gpu=0
// GPU:      gpu=9*gpu/(9*gpu+0.9*vCPU+0.1*mem), cpu=0.9*vCPU/denom, mem=0.1*mem/denom
func ComputeWeights(spec InstanceSpec) (cpuWeight, memWeight, gpuWeight float64) {
	vcpu := float64(spec.VCPUs)
	mem := spec.MemoryGiB
	gpuCount := float64(spec.GPUCount)

	if spec.GPUCount > 0 {
		denom := 9*gpuCount + 0.9*vcpu + 0.1*mem
		if denom == 0 {
			return 0, 0, 0
		}
		gpuWeight = (9 * gpuCount) / denom
		cpuWeight = (0.9 * vcpu) / denom
		memWeight = (0.1 * mem) / denom
	} else {
		denom := 9*vcpu + 1*mem
		if denom == 0 {
			return 0, 0, 0
		}
		cpuWeight = (9 * vcpu) / denom
		memWeight = (1 * mem) / denom
		gpuWeight = 0
	}
	return cpuWeight, memWeight, gpuWeight
}

// effectiveUsage returns min(max(reserved, utilization), 100).
// The min(..., 100) cap ensures used cost never exceeds total cost at the node level.
func effectiveUsage(reserved, utilization float64) float64 {
	return math.Min(math.Max(reserved, utilization), 100)
}

// computeUsedUnused splits a resource cost into used and unused portions.
// usedCost = (utilization / effectiveUsage) * resourceCost
// unusedCost = resourceCost - usedCost
func computeUsedUnused(resourceCost, utilization, effective float64) (usedCost, unusedCost float64) {
	if effective <= 0 || resourceCost <= 0 {
		return 0, resourceCost
	}
	ratio := utilization / effective
	if ratio > 1 {
		ratio = 1
	}
	usedCost = ratio * resourceCost
	unusedCost = resourceCost - usedCost
	return usedCost, unusedCost
}

// ComputeNodeCost computes the full cost breakdown for a single node.
func (ce *CostEngine) ComputeNodeCost(input NodeCostInput) NodeCost {
	cpuW, memW, gpuW := ComputeWeights(input.Spec)

	result := NodeCost{
		CPUCost: input.HourlyPrice * cpuW,
		MemCost: input.HourlyPrice * memW,
		GPUCost: input.HourlyPrice * gpuW,
	}

	// Used/unused splits based on effective usage.
	// node_cpu_used_cost = (node_cpu_split / 100) × Node CPU Cost
	cpuSplit := effectiveUsage(input.CPUReserved, input.CPUUtilization)
	result.CPUUsedCost = (cpuSplit / 100) * result.CPUCost
	result.CPUUnusedCost = result.CPUCost - result.CPUUsedCost

	memSplit := effectiveUsage(input.MemReserved, input.MemUtilization)
	result.MemUsedCost = (memSplit / 100) * result.MemCost
	result.MemUnusedCost = result.MemCost - result.MemUsedCost

	gpuSplit := effectiveUsage(input.GPUReserved, input.GPUUtilization)
	result.GPUUsedCost = (gpuSplit / 100) * result.GPUCost
	result.GPUUnusedCost = result.GPUCost - result.GPUUsedCost

	// Shared cost: cluster overhead distributed across nodes.
	if input.NodeCount > 0 {
		result.SharedCost = (input.ClusterFees.ClusterFee + input.ClusterFees.ProvisionedControlPlane) / float64(input.NodeCount)
	}

	// Total = CPU + Memory + GPU (shared is separate).
	result.TotalCost = result.CPUCost + result.MemCost + result.GPUCost

	return result
}

// ComputePodCosts computes cost for all pods on a single node.
func (ce *CostEngine) ComputePodCosts(nodeCost NodeCost, pods []PodCostInput) []PodCost {
	n := len(pods)
	if n == 0 {
		return nil
	}

	// Gather effective usage values per dimension.
	cpuEffectives := make([]float64, n)
	memEffectives := make([]float64, n)
	gpuEffectives := make([]float64, n)
	for i, p := range pods {
		cpuEffectives[i] = p.CPUEffective
		memEffectives[i] = p.MemEffective
		gpuEffectives[i] = p.GPUEffective
	}

	// Compute total effective for normalization denominator.
	cpuTotal, memTotal, gpuTotal := 0.0, 0.0, 0.0
	for i := range pods {
		cpuTotal += cpuEffectives[i]
		memTotal += memEffectives[i]
		gpuTotal += gpuEffectives[i]
	}

	// Normalize: if sum > 0, each pod's share = its effective / total.
	// If sum == 0, distribute equally.
	cpuShares := normalizeSharesByTotal(cpuEffectives, cpuTotal, n)
	memShares := normalizeSharesByTotal(memEffectives, memTotal, n)
	gpuShares := normalizeSharesByTotal(gpuEffectives, gpuTotal, n)

	results := make([]PodCost, n)
	for i, p := range pods {
		pc := PodCost{
			PodName:   p.PodName,
			Namespace: p.Namespace,
			NodeName:  p.NodeName,
		}

		pc.CPUCost = cpuShares[i] * nodeCost.CPUCost
		pc.MemCost = memShares[i] * nodeCost.MemCost
		pc.GPUCost = gpuShares[i] * nodeCost.GPUCost

		// Used cost = (utilization / 100) × Node Resource Cost (per refined doc).
		// Unused cost = Pod Resource Cost - Pod Used Resource Cost.
		pc.CPUUsedCost = (p.CPUUtilization / 100) * nodeCost.CPUCost
		pc.CPUUnusedCost = pc.CPUCost - pc.CPUUsedCost
		if pc.CPUUnusedCost < 0 {
			pc.CPUUnusedCost = 0
		}

		pc.MemUsedCost = (p.MemUtilization / 100) * nodeCost.MemCost
		pc.MemUnusedCost = pc.MemCost - pc.MemUsedCost
		if pc.MemUnusedCost < 0 {
			pc.MemUnusedCost = 0
		}

		pc.GPUUsedCost = (p.GPUUtilization / 100) * nodeCost.GPUCost
		pc.GPUUnusedCost = pc.GPUCost - pc.GPUUsedCost
		if pc.GPUUnusedCost < 0 {
			pc.GPUUnusedCost = 0
		}

		// Shared cost distributed equally among pods on the node.
		pc.SharedCost = nodeCost.SharedCost / float64(n)

		// Total = CPU + Memory + GPU (shared NOT included).
		pc.TotalCost = pc.CPUCost + pc.MemCost + pc.GPUCost

		// Carry forward node costs for container-level used cost calculation.
		pc.NodeCPUCost = nodeCost.CPUCost
		pc.NodeMemCost = nodeCost.MemCost
		pc.NodeGPUCost = nodeCost.GPUCost

		results[i] = pc
	}
	return results
}

// normalizeSharesByTotal computes proportional shares. If total is 0, distributes equally.
func normalizeSharesByTotal(effectives []float64, total float64, n int) []float64 {
	shares := make([]float64, n)
	if total == 0 {
		equal := 1.0 / float64(n)
		for i := range shares {
			shares[i] = equal
		}
		return shares
	}
	for i, e := range effectives {
		shares[i] = e / total
	}
	return shares
}

// ComputeContainerCosts computes cost for all containers within a single pod.
func (ce *CostEngine) ComputeContainerCosts(podCost PodCost, containers []ContainerCostInput) []ContainerCost {
	n := len(containers)
	if n == 0 {
		return nil
	}

	cpuEffectives := make([]float64, n)
	memEffectives := make([]float64, n)
	gpuEffectives := make([]float64, n)
	cpuTotal, memTotal, gpuTotal := 0.0, 0.0, 0.0
	for i, c := range containers {
		cpuEffectives[i] = c.CPUEffective
		memEffectives[i] = c.MemEffective
		gpuEffectives[i] = c.GPUEffective
		cpuTotal += c.CPUEffective
		memTotal += c.MemEffective
		gpuTotal += c.GPUEffective
	}

	cpuShares := normalizeSharesByTotal(cpuEffectives, cpuTotal, n)
	memShares := normalizeSharesByTotal(memEffectives, memTotal, n)
	gpuShares := normalizeSharesByTotal(gpuEffectives, gpuTotal, n)

	results := make([]ContainerCost, n)
	for i, c := range containers {
		cc := ContainerCost{
			ContainerName: c.ContainerName,
			PodName:       c.PodName,
			Namespace:     c.Namespace,
		}

		cc.CPUCost = cpuShares[i] * podCost.CPUCost
		cc.MemCost = memShares[i] * podCost.MemCost
		cc.GPUCost = gpuShares[i] * podCost.GPUCost

		// Used cost = (container_utilization / 100) × Node Resource Cost (per refined doc).
		// Unused cost = Container Resource Cost - Container Used Resource Cost.
		cc.CPUUsedCost = (c.CPUUtilization / 100) * podCost.NodeCPUCost
		cc.CPUUnusedCost = cc.CPUCost - cc.CPUUsedCost
		if cc.CPUUnusedCost < 0 {
			cc.CPUUnusedCost = 0
		}

		cc.MemUsedCost = (c.MemUtilization / 100) * podCost.NodeMemCost
		cc.MemUnusedCost = cc.MemCost - cc.MemUsedCost
		if cc.MemUnusedCost < 0 {
			cc.MemUnusedCost = 0
		}

		cc.GPUUsedCost = (c.GPUUtilization / 100) * podCost.NodeGPUCost
		cc.GPUUnusedCost = cc.GPUCost - cc.GPUUsedCost
		if cc.GPUUnusedCost < 0 {
			cc.GPUUnusedCost = 0
		}

		cc.SharedCost = podCost.SharedCost / float64(n)
		cc.TotalCost = cc.CPUCost + cc.MemCost + cc.GPUCost

		results[i] = cc
	}
	return results
}

// ComputeNamespaceCosts aggregates pod costs by namespace.
func (ce *CostEngine) ComputeNamespaceCosts(podCosts []PodCost) map[string]NamespaceCost {
	nsMap := make(map[string]*NamespaceCost)
	for _, pc := range podCosts {
		ns, ok := nsMap[pc.Namespace]
		if !ok {
			ns = &NamespaceCost{Namespace: pc.Namespace}
			nsMap[pc.Namespace] = ns
		}
		ns.CPUCost += pc.CPUCost
		ns.CPUUsedCost += pc.CPUUsedCost
		ns.CPUUnusedCost += pc.CPUUnusedCost
		ns.MemCost += pc.MemCost
		ns.MemUsedCost += pc.MemUsedCost
		ns.MemUnusedCost += pc.MemUnusedCost
		ns.GPUCost += pc.GPUCost
		ns.GPUUsedCost += pc.GPUUsedCost
		ns.GPUUnusedCost += pc.GPUUnusedCost
		ns.SharedCost += pc.SharedCost
	}

	result := make(map[string]NamespaceCost, len(nsMap))
	for k, ns := range nsMap {
		ns.TotalCost = ns.CPUCost + ns.MemCost + ns.GPUCost
		result[k] = *ns
	}
	return result
}

// ComputeClusterCost aggregates node costs into a cluster-level cost.
func (ce *CostEngine) ComputeClusterCost(nodeCosts []NodeCost, clusterFees ClusterFees) ClusterCost {
	cc := ClusterCost{}
	for _, nc := range nodeCosts {
		cc.CPUCost += nc.CPUCost
		cc.CPUUsedCost += nc.CPUUsedCost
		cc.CPUUnusedCost += nc.CPUUnusedCost
		cc.MemCost += nc.MemCost
		cc.MemUsedCost += nc.MemUsedCost
		cc.MemUnusedCost += nc.MemUnusedCost
		cc.GPUCost += nc.GPUCost
		cc.GPUUsedCost += nc.GPUUsedCost
		cc.GPUUnusedCost += nc.GPUUnusedCost
	}
	cc.OverheadCost = clusterFees.ClusterFee + clusterFees.ProvisionedControlPlane
	cc.TotalCost = cc.CPUCost + cc.MemCost + cc.GPUCost + cc.OverheadCost
	return cc
}
