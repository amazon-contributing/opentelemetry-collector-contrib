// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package kubelet

import (
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
	"testing"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	cTestUtils "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/testutils"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/testutils"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// MockKubeletProvider Mock provider implements KubeletProvider interface.
type MockKubeletProvider struct {
	logger *zap.Logger
	t      *testing.T
}

func (m *MockKubeletProvider) GetSummary() (*stats.Summary, error) {
	return testutils.LoadKubeletSummary(m.t, "./../extractors/testdata/CurSingleKubeletSummary.json"), nil
}

func createKubeletDecoratorWithMockKubeletProvider(t *testing.T, logger *zap.Logger) Options {
	return func(provider *SummaryProvider) {
		provider.kubeletProvider = &MockKubeletProvider{t: t, logger: logger}
	}
}

func mockInfoProvider() cTestUtils.MockHostInfo {
	hostInfo := cTestUtils.MockHostInfo{ClusterName: "cluster"}
	return hostInfo
}

func mockMetricExtractors() []extractors.MetricExtractor {
	metricsExtractors := []extractors.MetricExtractor{}
	metricsExtractors = append(metricsExtractors, extractors.NewCPUMetricExtractor(&zap.Logger{}))
	metricsExtractors = append(metricsExtractors, extractors.NewMemMetricExtractor(&zap.Logger{}))
	return metricsExtractors
}

// TestGetPodMetrics Verify tags on pod and container levels metrics.
func TestGetPodMetrics(t *testing.T) {

	k8sSummaryProvider, err := New(&zap.Logger{}, mockInfoProvider(), mockMetricExtractors(), createKubeletDecoratorWithMockKubeletProvider(t, &zap.Logger{}))
	summary, err := k8sSummaryProvider.kubeletProvider.GetSummary()
	metrics, err := k8sSummaryProvider.getPodMetrics(summary)

	assert.NoError(t, err)
	assert.NotNil(t, metrics)

	podMetric := metrics[1]
	assert.Equal(t, podMetric.GetMetricType(), ci.TypePod)
	assert.NotNil(t, podMetric.GetTag(ci.PodIDKey))
	assert.NotNil(t, podMetric.GetTag(ci.K8sPodNameKey))
	assert.NotNil(t, podMetric.GetTag(ci.K8sNamespace))
	assert.NotNil(t, podMetric.GetTag(ci.Timestamp))
	assert.NotNil(t, podMetric.GetTag(ci.SourcesKey))

	containerMetric := metrics[len(metrics)-1]
	assert.Equal(t, containerMetric.GetMetricType(), ci.TypeContainer)
	assert.NotNil(t, containerMetric.GetTag(ci.PodIDKey))
	assert.NotNil(t, containerMetric.GetTag(ci.K8sPodNameKey))
	assert.NotNil(t, containerMetric.GetTag(ci.K8sNamespace))
	assert.NotNil(t, containerMetric.GetTag(ci.Timestamp))
	assert.NotNil(t, containerMetric.GetTag(ci.ContainerNamekey))
	assert.NotNil(t, containerMetric.GetTag(ci.ContainerIDkey))
	assert.NotNil(t, containerMetric.GetTag(ci.SourcesKey))
}

// TestGetContainerMetrics verify tags on container level metrics returned.
func TestGetContainerMetrics(t *testing.T) {

	k8sSummaryProvider, err := New(&zap.Logger{}, mockInfoProvider(), mockMetricExtractors(), createKubeletDecoratorWithMockKubeletProvider(t, &zap.Logger{}))
	summary, err := k8sSummaryProvider.kubeletProvider.GetSummary()

	metrics, err := k8sSummaryProvider.getContainerMetrics(summary.Pods[0])
	assert.NoError(t, err)
	assert.NotNil(t, metrics)

	containerMetric := metrics[1]
	assert.Equal(t, containerMetric.GetMetricType(), ci.TypeContainer)
	assert.NotNil(t, containerMetric.GetTag(ci.PodIDKey))
	assert.NotNil(t, containerMetric.GetTag(ci.K8sPodNameKey))
	assert.NotNil(t, containerMetric.GetTag(ci.K8sNamespace))
	assert.NotNil(t, containerMetric.GetTag(ci.Timestamp))
	assert.NotNil(t, containerMetric.GetTag(ci.ContainerNamekey))
	assert.NotNil(t, containerMetric.GetTag(ci.ContainerIDkey))
	assert.NotNil(t, containerMetric.GetTag(ci.SourcesKey))
}

// TestGetNodeMetrics verify tags on node level metrics.
func TestGetNodeMetrics(t *testing.T) {

	k8sSummaryProvider, err := New(&zap.Logger{}, mockInfoProvider(), mockMetricExtractors(), createKubeletDecoratorWithMockKubeletProvider(t, &zap.Logger{}))
	summary, err := k8sSummaryProvider.kubeletProvider.GetSummary()

	metrics, err := k8sSummaryProvider.getNodeMetrics(summary)
	assert.NoError(t, err)
	assert.NotNil(t, metrics)

	nodeMetric := metrics[1]
	assert.Equal(t, nodeMetric.GetMetricType(), ci.TypeNode)
	assert.NotNil(t, nodeMetric.GetTag(ci.SourcesKey))
}