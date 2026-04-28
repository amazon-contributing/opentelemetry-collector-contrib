// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"context"
	"errors"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// MetricsDataProvider provides the ECI metrics data needed for cost computation.
// This interface decouples the cost allocator from the specific metrics source.
type MetricsDataProvider interface {
	// GetNodeData returns cost input data for all nodes in the cluster.
	GetNodeData() ([]NodeCostInput, []NodeAttributes)
	// GetPodData returns cost input data for all pods on a given node.
	GetPodData(nodeName string) []PodCostInput
	// GetContainerData returns cost input data for all containers in a given pod.
	GetContainerData(podName, namespace string) []ContainerCostInput
	// GetNodeCount returns the total number of nodes in the cluster.
	GetNodeCount() int
}

// CostAllocatorOpts holds the options for creating a CostAllocator.
type CostAllocatorOpts struct {
	Logger          *zap.Logger
	PricingClient   *PricingClient
	InstanceClient  *InstanceInfoClient
	ClusterClient   *ClusterInfoClient
	MetricsProvider MetricsDataProvider
	ClusterName     string
}

// CostAllocator orchestrates cost computation and metric emission.
// It implements the metricsProvider interface (GetMetrics/Shutdown).
type CostAllocator struct {
	logger          *zap.Logger
	pricingClient   *PricingClient
	instanceClient  *InstanceInfoClient
	clusterClient   *ClusterInfoClient
	metricsProvider MetricsDataProvider
	costEngine      *CostEngine
	metricBuilder   *MetricBuilder
}

// NewCostAllocator creates a new CostAllocator.
func NewCostAllocator(opts CostAllocatorOpts) (*CostAllocator, error) {
	if opts.PricingClient == nil || opts.InstanceClient == nil || opts.ClusterClient == nil {
		return nil, errors.New("all AWS clients (pricing, instance, cluster) are required")
	}
	if opts.MetricsProvider == nil {
		return nil, errors.New("metrics data provider is required")
	}
	return &CostAllocator{
		logger:          opts.Logger,
		pricingClient:   opts.PricingClient,
		instanceClient:  opts.InstanceClient,
		clusterClient:   opts.ClusterClient,
		metricsProvider: opts.MetricsProvider,
		costEngine:      NewCostEngine(opts.Logger),
		metricBuilder:   NewMetricBuilder(opts.ClusterName),
	}, nil
}

// Start initializes all client refresh goroutines.
func (ca *CostAllocator) Start(ctx context.Context) {
	ca.pricingClient.Start(ctx)
	ca.instanceClient.Start(ctx)
	ca.clusterClient.Start(ctx)
}

// GetMetrics computes cost allocation metrics for the current collection interval.
func (ca *CostAllocator) GetMetrics() []pmetric.Metrics {
	nodeInputs, nodeAttrs := ca.metricsProvider.GetNodeData()
	if len(nodeInputs) == 0 {
		return nil
	}

	clusterFees := ca.clusterClient.GetFees()
	nodeCount := ca.metricsProvider.GetNodeCount()

	var allMetrics []pmetric.Metrics
	var allNodeCosts []NodeCost
	var allPodCosts []PodCost

	for i, input := range nodeInputs {
		// Resolve pricing.
		price, ok := ca.pricingClient.GetPrice(input.InstanceType)
		if !ok {
			ca.logger.Warn("No pricing data for instance type, skipping node",
				zap.String("instanceType", input.InstanceType),
				zap.String("nodeName", nodeAttrs[i].NodeName))
			continue
		}
		input.HourlyPrice = price

		// Resolve instance spec.
		spec, ok := ca.instanceClient.GetSpec(input.InstanceType)
		if !ok {
			ca.logger.Warn("No instance spec for instance type, skipping node",
				zap.String("instanceType", input.InstanceType),
				zap.String("nodeName", nodeAttrs[i].NodeName))
			continue
		}
		input.Spec = spec
		input.ClusterFees = clusterFees
		input.NodeCount = nodeCount

		// Compute node cost.
		nodeCost := ca.costEngine.ComputeNodeCost(input)
		allNodeCosts = append(allNodeCosts, nodeCost)
		allMetrics = append(allMetrics, ca.metricBuilder.BuildNodeMetrics(nodeCost, nodeAttrs[i]))

		// Compute pod costs for this node.
		podInputs := ca.metricsProvider.GetPodData(nodeAttrs[i].NodeName)
		podCosts := ca.costEngine.ComputePodCosts(nodeCost, podInputs)
		allPodCosts = append(allPodCosts, podCosts...)

		if len(podCosts) > 0 {
			allMetrics = append(allMetrics, ca.metricBuilder.BuildPodMetrics(podCosts))
		}

		// Compute container costs for each pod.
		for _, pc := range podCosts {
			containerInputs := ca.metricsProvider.GetContainerData(pc.PodName, pc.Namespace)
			if len(containerInputs) == 0 {
				continue
			}
			containerCosts := ca.costEngine.ComputeContainerCosts(pc, containerInputs)
			allMetrics = append(allMetrics, ca.metricBuilder.BuildContainerMetrics(containerCosts))
		}
	}

	// Namespace aggregation.
	if len(allPodCosts) > 0 {
		nsCosts := ca.costEngine.ComputeNamespaceCosts(allPodCosts)
		allMetrics = append(allMetrics, ca.metricBuilder.BuildNamespaceMetrics(nsCosts))
	}

	// Cluster aggregation.
	if len(allNodeCosts) > 0 {
		clusterCost := ca.costEngine.ComputeClusterCost(allNodeCosts, clusterFees)
		allMetrics = append(allMetrics, ca.metricBuilder.BuildClusterMetrics(clusterCost))
	}

	return allMetrics
}

// Shutdown stops all client refresh goroutines.
func (ca *CostAllocator) Shutdown() error {
	ca.pricingClient.Shutdown()
	ca.instanceClient.Shutdown()
	ca.clusterClient.Shutdown()
	return nil
}
