// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const metricUnit = "USD/hr"

// MetricBuilder converts cost structs into pmetric.Metrics.
type MetricBuilder struct {
	clusterName string
}

// NewMetricBuilder creates a new MetricBuilder.
func NewMetricBuilder(clusterName string) *MetricBuilder {
	return &MetricBuilder{clusterName: clusterName}
}

// addGauge adds a single gauge metric data point to the given scope metrics.
func addGauge(sm pmetric.ScopeMetrics, name string, value float64, ts time.Time, attrs map[string]string) {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(metricUnit)
	g := m.SetEmptyGauge()
	dp := g.DataPoints().AppendEmpty()
	dp.SetDoubleValue(value)
	dp.SetTimestamp(pcommon.NewTimestampFromTime(ts))
	for k, v := range attrs {
		dp.Attributes().PutStr(k, v)
	}
}

// BuildNodeMetrics builds OTel metrics for a single node's cost breakdown.
func (mb *MetricBuilder) BuildNodeMetrics(nc NodeCost, attrs NodeAttributes) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	ts := time.Now()

	a := map[string]string{
		"ClusterName": mb.clusterName,
		"InstanceId":  attrs.InstanceID,
		"NodeName":    attrs.NodeName,
	}

	addGauge(sm, "node_cpu_cost", nc.CPUCost, ts, a)
	addGauge(sm, "node_cpu_used_cost", nc.CPUUsedCost, ts, a)
	addGauge(sm, "node_cpu_unused_cost", nc.CPUUnusedCost, ts, a)
	addGauge(sm, "node_memory_cost", nc.MemCost, ts, a)
	addGauge(sm, "node_memory_used_cost", nc.MemUsedCost, ts, a)
	addGauge(sm, "node_memory_unused_cost", nc.MemUnusedCost, ts, a)
	addGauge(sm, "node_gpu_cost", nc.GPUCost, ts, a)
	addGauge(sm, "node_gpu_used_cost", nc.GPUUsedCost, ts, a)
	addGauge(sm, "node_gpu_unused_cost", nc.GPUUnusedCost, ts, a)
	addGauge(sm, "node_shared_cost", nc.SharedCost, ts, a)
	addGauge(sm, "node_total_cost", nc.TotalCost, ts, a)

	return md
}

// BuildPodMetrics builds OTel metrics for a slice of pod costs.
func (mb *MetricBuilder) BuildPodMetrics(podCosts []PodCost) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	ts := time.Now()

	for _, pc := range podCosts {
		a := map[string]string{
			"ClusterName": mb.clusterName,
			"Namespace":   pc.Namespace,
			"PodName":     pc.PodName,
		}
		addGauge(sm, "pod_cpu_cost", pc.CPUCost, ts, a)
		addGauge(sm, "pod_cpu_used_cost", pc.CPUUsedCost, ts, a)
		addGauge(sm, "pod_cpu_unused_cost", pc.CPUUnusedCost, ts, a)
		addGauge(sm, "pod_memory_cost", pc.MemCost, ts, a)
		addGauge(sm, "pod_memory_used_cost", pc.MemUsedCost, ts, a)
		addGauge(sm, "pod_memory_unused_cost", pc.MemUnusedCost, ts, a)
		addGauge(sm, "pod_gpu_cost", pc.GPUCost, ts, a)
		addGauge(sm, "pod_gpu_used_cost", pc.GPUUsedCost, ts, a)
		addGauge(sm, "pod_gpu_unused_cost", pc.GPUUnusedCost, ts, a)
		addGauge(sm, "pod_shared_cost", pc.SharedCost, ts, a)
		addGauge(sm, "pod_total_cost", pc.TotalCost, ts, a)
	}
	return md
}

// BuildContainerMetrics builds OTel metrics for a slice of container costs.
func (mb *MetricBuilder) BuildContainerMetrics(containerCosts []ContainerCost) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	ts := time.Now()

	for _, cc := range containerCosts {
		a := map[string]string{
			"ClusterName":   mb.clusterName,
			"Namespace":     cc.Namespace,
			"PodName":       cc.PodName,
			"ContainerName": cc.ContainerName,
		}
		addGauge(sm, "container_cpu_cost", cc.CPUCost, ts, a)
		addGauge(sm, "container_cpu_used_cost", cc.CPUUsedCost, ts, a)
		addGauge(sm, "container_cpu_unused_cost", cc.CPUUnusedCost, ts, a)
		addGauge(sm, "container_memory_cost", cc.MemCost, ts, a)
		addGauge(sm, "container_memory_used_cost", cc.MemUsedCost, ts, a)
		addGauge(sm, "container_memory_unused_cost", cc.MemUnusedCost, ts, a)
		addGauge(sm, "container_gpu_cost", cc.GPUCost, ts, a)
		addGauge(sm, "container_gpu_used_cost", cc.GPUUsedCost, ts, a)
		addGauge(sm, "container_gpu_unused_cost", cc.GPUUnusedCost, ts, a)
		addGauge(sm, "container_shared_cost", cc.SharedCost, ts, a)
		addGauge(sm, "container_total_cost", cc.TotalCost, ts, a)
	}
	return md
}

// BuildNamespaceMetrics builds OTel metrics for namespace-level costs.
func (mb *MetricBuilder) BuildNamespaceMetrics(nsCosts map[string]NamespaceCost) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	ts := time.Now()

	for _, ns := range nsCosts {
		a := map[string]string{
			"ClusterName": mb.clusterName,
			"Namespace":   ns.Namespace,
		}
		addGauge(sm, "namespace_cpu_cost", ns.CPUCost, ts, a)
		addGauge(sm, "namespace_cpu_used_cost", ns.CPUUsedCost, ts, a)
		addGauge(sm, "namespace_cpu_unused_cost", ns.CPUUnusedCost, ts, a)
		addGauge(sm, "namespace_memory_cost", ns.MemCost, ts, a)
		addGauge(sm, "namespace_memory_used_cost", ns.MemUsedCost, ts, a)
		addGauge(sm, "namespace_memory_unused_cost", ns.MemUnusedCost, ts, a)
		addGauge(sm, "namespace_gpu_cost", ns.GPUCost, ts, a)
		addGauge(sm, "namespace_gpu_used_cost", ns.GPUUsedCost, ts, a)
		addGauge(sm, "namespace_gpu_unused_cost", ns.GPUUnusedCost, ts, a)
		addGauge(sm, "namespace_shared_cost", ns.SharedCost, ts, a)
		addGauge(sm, "namespace_total_cost", ns.TotalCost, ts, a)
	}
	return md
}

// BuildClusterMetrics builds OTel metrics for cluster-level costs.
func (mb *MetricBuilder) BuildClusterMetrics(cc ClusterCost) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	ts := time.Now()

	a := map[string]string{
		"ClusterName": mb.clusterName,
	}

	addGauge(sm, "cluster_cpu_cost", cc.CPUCost, ts, a)
	addGauge(sm, "cluster_cpu_used_cost", cc.CPUUsedCost, ts, a)
	addGauge(sm, "cluster_cpu_unused_cost", cc.CPUUnusedCost, ts, a)
	addGauge(sm, "cluster_memory_cost", cc.MemCost, ts, a)
	addGauge(sm, "cluster_memory_used_cost", cc.MemUsedCost, ts, a)
	addGauge(sm, "cluster_memory_unused_cost", cc.MemUnusedCost, ts, a)
	addGauge(sm, "cluster_gpu_cost", cc.GPUCost, ts, a)
	addGauge(sm, "cluster_gpu_used_cost", cc.GPUUsedCost, ts, a)
	addGauge(sm, "cluster_gpu_unused_cost", cc.GPUUnusedCost, ts, a)
	addGauge(sm, "cluster_overhead_cost", cc.OverheadCost, ts, a)
	addGauge(sm, "cluster_total_cost", cc.TotalCost, ts, a)

	return md
}
