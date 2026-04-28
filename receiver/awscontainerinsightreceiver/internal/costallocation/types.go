// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

// HostInfoProvider is the subset of host.Info needed by cost allocation.
type HostInfoProvider interface {
	GetInstanceType() string
	GetRegion() string
	GetClusterName() string
}

// InstanceSpec holds the hardware specifications for an EC2 instance type.
type InstanceSpec struct {
	VCPUs     int64
	MemoryGiB float64
	GPUCount  int64
}

// ClusterFees holds the EKS control plane fee components.
type ClusterFees struct {
	ClusterFee              float64 // $0.10 or $0.60/hr based on support type
	ProvisionedControlPlane float64 // $0, $1.65, $3.40, $6.90, or $13.90/hr
}

// NodeCostInput contains all data needed to compute a single node's costs.
type NodeCostInput struct {
	InstanceType   string
	HourlyPrice    float64
	Spec           InstanceSpec
	ClusterFees    ClusterFees
	NodeCount      int
	CPUUtilization float64 // actual usage ratio
	MemUtilization float64 // actual usage ratio
	GPUUtilization float64
	CPUCapacity    float64
	MemCapacity    float64
	GPUCapacity    float64
	CPUReserved    float64 // sum of pod requests
	MemReserved    float64
	GPUReserved    float64
}

// NodeCost holds the computed cost breakdown for a single node.
type NodeCost struct {
	CPUCost       float64
	CPUUsedCost   float64
	CPUUnusedCost float64
	MemCost       float64
	MemUsedCost   float64
	MemUnusedCost float64
	GPUCost       float64
	GPUUsedCost   float64
	GPUUnusedCost float64
	SharedCost    float64
	TotalCost     float64 // cpu_cost + memory_cost + gpu_cost (shared NOT included)
}

// PodCostInput is the input for computing a single pod's cost.
type PodCostInput struct {
	PodName        string
	Namespace      string
	NodeName       string
	CPUEffective   float64 // max(requests, utilization)
	MemEffective   float64
	GPUEffective   float64
	CPUUtilization float64
	MemUtilization float64
	GPUUtilization float64
	ContainerCount int
}

// PodCost holds the computed cost breakdown for a single pod.
type PodCost struct {
	PodName       string
	Namespace     string
	NodeName      string
	CPUCost       float64
	CPUUsedCost   float64
	CPUUnusedCost float64
	MemCost       float64
	MemUsedCost   float64
	MemUnusedCost float64
	GPUCost       float64
	GPUUsedCost   float64
	GPUUnusedCost float64
	SharedCost    float64
	TotalCost     float64 // cpu_cost + memory_cost + gpu_cost (shared NOT included)
	// NodeCPUCost, NodeMemCost, NodeGPUCost are carried forward for container-level used cost calculation.
	NodeCPUCost float64
	NodeMemCost float64
	NodeGPUCost float64
}

// ContainerCostInput is the input for computing a single container's cost.
type ContainerCostInput struct {
	ContainerName  string
	PodName        string
	Namespace      string
	CPUEffective   float64
	MemEffective   float64
	GPUEffective   float64
	CPUUtilization float64
	MemUtilization float64
	GPUUtilization float64
}

// ContainerCost holds the computed cost breakdown for a single container.
type ContainerCost struct {
	ContainerName string
	PodName       string
	Namespace     string
	CPUCost       float64
	CPUUsedCost   float64
	CPUUnusedCost float64
	MemCost       float64
	MemUsedCost   float64
	MemUnusedCost float64
	GPUCost       float64
	GPUUsedCost   float64
	GPUUnusedCost float64
	SharedCost    float64
	TotalCost     float64 // cpu_cost + memory_cost + gpu_cost (shared NOT included)
}

// NamespaceCost holds the aggregated cost for a Kubernetes namespace.
type NamespaceCost struct {
	Namespace     string
	CPUCost       float64
	CPUUsedCost   float64
	CPUUnusedCost float64
	MemCost       float64
	MemUsedCost   float64
	MemUnusedCost float64
	GPUCost       float64
	GPUUsedCost   float64
	GPUUnusedCost float64
	SharedCost    float64
	TotalCost     float64 // cpu_cost + memory_cost + gpu_cost (shared NOT included)
}

// ClusterCost holds the aggregated cost for the entire cluster.
type ClusterCost struct {
	CPUCost       float64
	CPUUsedCost   float64
	CPUUnusedCost float64
	MemCost       float64
	MemUsedCost   float64
	MemUnusedCost float64
	GPUCost       float64
	GPUUsedCost   float64
	GPUUnusedCost float64
	OverheadCost  float64 // ClusterFee + ProvisionedControlPlaneFee
	TotalCost     float64 // cpu_cost + memory_cost + gpu_cost + overhead_cost
}

// NodeAttributes holds the metric dimension data for node-level metrics.
type NodeAttributes struct {
	ClusterName string
	InstanceID  string
	NodeName    string
}

// PodAttributes holds the metric dimension data for pod-level metrics.
type PodAttributes struct {
	ClusterName string
	Namespace   string
	PodName     string
}

// ContainerAttributes holds the metric dimension data for container-level metrics.
type ContainerAttributes struct {
	ClusterName   string
	Namespace     string
	PodName       string
	ContainerName string
}
