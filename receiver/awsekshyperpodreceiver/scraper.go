// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsekshyperpodreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsekshyperpodreceiver"

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sutil"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsekshyperpodreceiver/internal/metadata"
)

const hyperPodPrefix = "hyperpod-"

// clusterNameAttribute is the name of the cluster_name data point attribute.
const clusterNameAttribute = "cluster_name"

var allStatuses = []k8sutil.HyperPodConditionType{
	k8sutil.Schedulable,
	k8sutil.UnschedulablePendingReplacement,
	k8sutil.UnschedulablePendingReboot,
	k8sutil.Unschedulable,
}

type scraper struct {
	config     *Config
	logger     *zap.Logger
	mb         *metadata.MetricsBuilder
	k8sClient  *k8sclient.K8sClient
	nodeClient k8sclient.NodeClient
}

func newScraper(config *Config, settings receiver.Settings) *scraper {
	return &scraper{
		config: config,
		logger: settings.Logger,
		mb:     metadata.NewMetricsBuilder(config.MetricsBuilderConfig, settings),
	}
}

func (s *scraper) start(_ context.Context, _ component.Host) error {
	s.logger.Info("Starting HyperPod health receiver",
		zap.String("cluster", s.config.ClusterName),
		zap.Duration("interval", s.config.CollectionInterval),
	)

	client := k8sclient.Get(s.logger, k8sclient.CaptureOnlyNodeLabelsInfo(true))
	if client == nil {
		return errors.New("failed to initialize K8s client")
	}
	s.k8sClient = client
	s.nodeClient = client.GetNodeClient()

	return nil
}

func (s *scraper) shutdown(_ context.Context) error {
	s.logger.Info("Shutting down HyperPod health receiver")
	if s.k8sClient != nil {
		s.k8sClient.Shutdown()
	}
	return nil
}

func (s *scraper) scrape(_ context.Context) (pmetric.Metrics, error) {
	nodeToLabelsMap := s.nodeClient.NodeToLabelsMap()

	s.logger.Debug("Collected nodes",
		zap.Int("nodesWithLabels", len(nodeToLabelsMap)),
	)

	if len(nodeToLabelsMap) == 0 {
		return pmetric.NewMetrics(), nil
	}

	now := pcommon.NewTimestampFromTime(time.Now())
	nodeCount := 0
	for nodeName, labelsMap := range nodeToLabelsMap {
		if s.processNode(nodeName, labelsMap, now) {
			nodeCount++
		}
	}

	// If no nodes produced metrics (e.g., all had invalid/missing health labels),
	// return empty metrics to avoid publishing empty ResourceMetrics/ScopeMetrics.
	if nodeCount == 0 {
		return pmetric.NewMetrics(), nil
	}

	metrics := s.mb.Emit()

	// cluster_name is config-derived and constant across the scrape. When no
	// cluster name is configured, omit the attribute entirely rather than
	// emitting an empty value.
	if s.config.ClusterName == "" {
		removeAttributeFromDataPoints(metrics, clusterNameAttribute)
	}

	return metrics, nil
}

func (s *scraper) processNode(nodeName string, labelsMap map[k8sclient.Label]int8, timestamp pcommon.Timestamp) bool {
	// Get health status from labels map.
	healthStatusInt, ok := labelsMap[k8sclient.SageMakerNodeHealthStatus]
	if !ok {
		s.logger.Debug("Node missing health status label",
			zap.String("node", nodeName),
		)
		return false
	}

	// Validate health status value.
	if !isValidHealthStatus(healthStatusInt) {
		s.logger.Warn("Invalid health status value",
			zap.String("node", nodeName),
			zap.Int8("status", healthStatusInt),
		)
		return false
	}

	// Convert int8 to status string.
	healthStatus := k8sutil.HyperPodConditionType(healthStatusInt).String()

	// Extract instance ID (remove hyperpod- prefix if present).
	instanceID := strings.TrimPrefix(nodeName, hyperPodPrefix)

	// Emit data points for all statuses (1 for current, 0 for others).
	s.emitHealthMetrics(nodeName, instanceID, healthStatus, timestamp)
	return true
}

// emitHealthMetrics records a one-hot encoding across all statuses: value 1 for
// the node's current status and 0 for every other status.
func (s *scraper) emitHealthMetrics(nodeName, instanceID, currentStatus string, timestamp pcommon.Timestamp) {
	clusterName := s.config.ClusterName
	for _, status := range allStatuses {
		value := int64(0)
		if status.String() == currentStatus {
			value = 1
		}
		s.recordStatus(status, timestamp, value, clusterName, instanceID, nodeName)
	}
}

// recordStatus dispatches to the generated MetricsBuilder method for the status.
func (s *scraper) recordStatus(status k8sutil.HyperPodConditionType, ts pcommon.Timestamp, val int64, clusterName, instanceID, nodeName string) {
	switch status {
	case k8sutil.Schedulable:
		s.mb.RecordHyperpodNodeHealthStatusSchedulableDataPoint(ts, val, clusterName, instanceID, nodeName)
	case k8sutil.UnschedulablePendingReplacement:
		s.mb.RecordHyperpodNodeHealthStatusUnschedulablePendingReplacementDataPoint(ts, val, clusterName, instanceID, nodeName)
	case k8sutil.UnschedulablePendingReboot:
		s.mb.RecordHyperpodNodeHealthStatusUnschedulablePendingRebootDataPoint(ts, val, clusterName, instanceID, nodeName)
	case k8sutil.Unschedulable:
		s.mb.RecordHyperpodNodeHealthStatusUnschedulableDataPoint(ts, val, clusterName, instanceID, nodeName)
	}
}

// removeAttributeFromDataPoints removes the named attribute from every gauge
// data point in the metrics.
func removeAttributeFromDataPoints(metrics pmetric.Metrics, attr string) {
	rms := metrics.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		sms := rms.At(i).ScopeMetrics()
		for j := 0; j < sms.Len(); j++ {
			ms := sms.At(j).Metrics()
			for k := 0; k < ms.Len(); k++ {
				if ms.At(k).Type() != pmetric.MetricTypeGauge {
					continue
				}
				dps := ms.At(k).Gauge().DataPoints()
				for l := 0; l < dps.Len(); l++ {
					dps.At(l).Attributes().Remove(attr)
				}
			}
		}
	}
}

func isValidHealthStatus(status int8) bool {
	return status >= int8(k8sutil.Schedulable) && status <= int8(k8sutil.Unschedulable)
}
