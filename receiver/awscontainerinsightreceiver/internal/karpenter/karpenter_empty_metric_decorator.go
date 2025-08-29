// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package karpenter

import (
	"context"
	"os"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
)

// IsKarpenterMetric checks if a metric name belongs to Karpenter
func IsKarpenterMetric(metricName string, metricType string) bool {
	// Direct karpenter metrics
	if len(metricName) >= 9 && metricName[:9] == "karpenter" {
		return true
	}
	// controller_runtime metrics are considered Karpenter metrics for cluster type
	if len(metricName) >= 18 && metricName[:18] == "controller_runtime" && metricType == ci.TypeCluster {
		return true
	}
	return false
}

const (
	nodePool     = "nodepool"
	provisioner  = "provisioner"
	nodeState    = "state"
	actionType   = "action"
	DefaultValue = "DEFAULT"
)

var attributeConfig = map[string][]string{
	KarpenterPodsStartupTime:                  {},
	KarpenterDeprovisioningReplacementMachine: {},
	KarpenterDisruptionReplacementNodeclaim:   {},
	KarpenterCloudproviderDuration:            {},
	KarpenterNodepoolLimit:                    {nodePool},
	KarpenterNodepoolUsage:                    {nodePool},
	KarpenterProvisionerLimit:                 {provisioner},
	KarpenterProvisionerUsage:                 {provisioner},
}

var defaultAttributeValues = map[string]string{
	nodePool:    "default",
	provisioner: "default",
	nodeState:   "ready",
	actionType:  "terminate",
}

type EmptyMetricDecorator struct {
	NextConsumer consumer.Metrics
	Logger       *zap.Logger
	MetricType   string
}

func (ed *EmptyMetricDecorator) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{
		MutatesData: true,
	}
}

func (ed *EmptyMetricDecorator) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rs := rms.At(i)

		// Ensure resource attributes for EMF exporter
		resourceTags := make(map[string]string)
		ras := rs.Resource().Attributes()
		ras.Range(func(k string, v pcommon.Value) bool {
			resourceTags[k] = v.AsString()
			return true
		})
		ed.ensureResourceAttributes(ras, resourceTags)

		ilms := rs.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			ils := ilms.At(j)
			metrics := ils.Metrics()

			// Filter out problematic metrics before processing
			ed.filterUnsupportedMetrics(metrics)

			ed.addEmptyMetrics(metrics)
			ed.addServiceTypeLabels(metrics)
		}
	}
	return ed.NextConsumer.ConsumeMetrics(ctx, md)
}

func (ed *EmptyMetricDecorator) addEmptyMetrics(metrics pmetric.MetricSlice) {
	metricFoundMap := make(map[string]bool)
	for k := range attributeConfig {
		metricFoundMap[k] = false
	}

	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		if _, ok := metricFoundMap[m.Name()]; ok {
			metricFoundMap[m.Name()] = true
		}
	}

	for k, v := range metricFoundMap {
		if !v {
			populateEmptyMetric(metrics, k, attributeConfig[k])
		}
	}
}

func populateEmptyMetric(metrics pmetric.MetricSlice, metricName string, attributesToAdd []string) {
	metricToAdd := pmetric.NewMetric()
	metricToAdd.SetEmptyGauge()
	metricToAdd.SetName(metricName)

	datapoint := metricToAdd.Gauge().DataPoints().AppendEmpty()
	datapoint.SetDoubleValue(0)

	for _, attribute := range attributesToAdd {
		if value, exists := defaultAttributeValues[attribute]; exists {
			datapoint.Attributes().PutStr(attribute, value)
		}
	}

	metricToAdd.CopyTo(metrics.AppendEmpty())
}

// addServiceTypeLabels adds ServiceType label to Karpenter metrics
func (ed *EmptyMetricDecorator) addServiceTypeLabels(metrics pmetric.MetricSlice) {
	for i := 0; i < metrics.Len(); i++ {
		m := metrics.At(i)
		if IsKarpenterMetric(m.Name(), ed.MetricType) {
			ed.addServiceTypeLabel(m, "Karpenter")
			ed.Logger.Debug("Added ServiceType label to Karpenter metric", zap.String("name", m.Name()))
		}
	}
}

// addServiceTypeLabel adds ServiceType label to a specific metric
func (ed *EmptyMetricDecorator) addServiceTypeLabel(m pmetric.Metric, serviceType string) {
	var dps pmetric.NumberDataPointSlice
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps = m.Gauge().DataPoints()
	case pmetric.MetricTypeSum:
		dps = m.Sum().DataPoints()
	default:
		return
	}

	for i := 0; i < dps.Len(); i++ {
		attrs := dps.At(i).Attributes()
		attrs.PutStr("ServiceType", serviceType)
	}
}

// filterUnsupportedMetrics removes histogram and other unsupported metric types that cause panics
func (ed *EmptyMetricDecorator) filterUnsupportedMetrics(metrics pmetric.MetricSlice) {
	// Create a new slice to hold only supported metrics
	originalLen := metrics.Len()
	writeIndex := 0

	for i := 0; i < originalLen; i++ {
		m := metrics.At(i)
		metricType := m.Type()

		// Only allow Gauge and Sum metrics, drop everything else (Histogram, Summary, etc.)
		if metricType == pmetric.MetricTypeGauge || metricType == pmetric.MetricTypeSum {
			// Keep this metric - move it to the write position if needed
			if writeIndex != i {
				m.CopyTo(metrics.At(writeIndex))
			}
			writeIndex++
		} else {
			ed.Logger.Debug("Filtering out unsupported metric type",
				zap.String("name", m.Name()),
				zap.String("type", metricType.String()))
		}
	}

	// Remove the extra metrics at the end
	for i := originalLen - 1; i >= writeIndex; i-- {
		metrics.RemoveIf(func(pmetric.Metric) bool { return true })
	}
}

// ensureResourceAttributes ensures essential resource attributes are present for EMF exporter compatibility
func (ed *EmptyMetricDecorator) ensureResourceAttributes(ras pcommon.Map, resourceTags map[string]string) {
	// Ensure ClusterName is present - this is critical for EMF exporter
	if _, exists := resourceTags[ci.ClusterNameKey]; !exists {
		if clusterName, exists := resourceTags["ClusterName"]; exists {
			ras.PutStr(ci.ClusterNameKey, clusterName)
			ed.Logger.Info("Added ClusterName resource attribute", zap.String("cluster", clusterName))
		} else {
			// Try to get cluster name from environment variables as fallback
			clusterName := ""

			// Check K8S_CLUSTER_NAME environment variable first
			if envClusterName := os.Getenv("K8S_CLUSTER_NAME"); envClusterName != "" {
				clusterName = envClusterName
				ed.Logger.Info("Using cluster name from K8S_CLUSTER_NAME environment variable", zap.String("cluster", clusterName))
			} else if envClusterName := os.Getenv("CLUSTER_NAME"); envClusterName != "" {
				clusterName = envClusterName
				ed.Logger.Info("Using cluster name from CLUSTER_NAME environment variable", zap.String("cluster", clusterName))
			}

			if clusterName != "" {
				ras.PutStr(ci.ClusterNameKey, clusterName)
				resourceTags[ci.ClusterNameKey] = clusterName
			}
		}
	}

	// Ensure other essential attributes are present
	if _, exists := resourceTags[ci.NodeNameKey]; !exists {
		if nodeName, exists := resourceTags["NodeName"]; exists {
			ras.PutStr(ci.NodeNameKey, nodeName)
		} else if nodeName := os.Getenv("HOST_NAME"); nodeName != "" {
			ras.PutStr(ci.NodeNameKey, nodeName)
			resourceTags[ci.NodeNameKey] = nodeName
		}
	}

	// Ensure Type is set for Container Insights
	if _, exists := resourceTags[ci.MetricType]; !exists {
		ras.PutStr(ci.MetricType, ed.MetricType)
		resourceTags[ci.MetricType] = ed.MetricType
	}
}
