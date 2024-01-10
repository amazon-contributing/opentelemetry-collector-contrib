// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package k8swindows

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"
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

func (m *MockKubeletProvider) GetClient() (*kubeletutil.KubeletClient, error) {
	return nil, nil
}

func (m *MockKubeletProvider) GetSummary() (*stats.Summary, error) {
	return testutils.LoadKubeletSummary(m.t, "./extractors/testdata/CurSingleKubeletSummary.json"), nil
}

func createKubeletDecoratorWithMockKubeletProvider(t *testing.T, logger *zap.Logger) options {
	return func(provider *kubelet) {
		provider.kubeletProvider = &MockKubeletProvider{t: t, logger: logger}
	}
}

func mockKubeletDependencies() cTestUtils.MockHostInfo {
	hostInfo := cTestUtils.MockHostInfo{ClusterName: "cluster"}
	metricsExtractors = []extractors.MetricExtractor{}
	metricsExtractors = append(metricsExtractors, extractors.NewCPUMetricExtractor(&zap.Logger{}))
	metricsExtractors = append(metricsExtractors, extractors.NewMemMetricExtractor(&zap.Logger{}))
	return hostInfo
}

// TestGetPodMetrics Verify tags on pod and container levels metrics.
func TestGetPodMetrics(t *testing.T) {
	hostInfo := mockKubeletDependencies()

	k8sSummaryProvider, err := new(&zap.Logger{}, hostInfo, createKubeletDecoratorWithMockKubeletProvider(t, &zap.Logger{}))
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

	containerMetric := metrics[len(metrics)-1]
	assert.Equal(t, containerMetric.GetMetricType(), ci.TypeContainer)
	assert.NotNil(t, containerMetric.GetTag(ci.PodIDKey))
	assert.NotNil(t, containerMetric.GetTag(ci.K8sPodNameKey))
	assert.NotNil(t, containerMetric.GetTag(ci.K8sNamespace))
	assert.NotNil(t, containerMetric.GetTag(ci.Timestamp))
	assert.NotNil(t, containerMetric.GetTag(ci.ContainerNamekey))
	assert.NotNil(t, containerMetric.GetTag(ci.ContainerIDkey))
}

// TestGetContainerMetrics verify tags on container level metrics returned.
func TestGetContainerMetrics(t *testing.T) {
	hostInfo := mockKubeletDependencies()

	k8sSummaryProvider, err := new(&zap.Logger{}, hostInfo, createKubeletDecoratorWithMockKubeletProvider(t, &zap.Logger{}))
	summary, err := k8sSummaryProvider.kubeletProvider.GetSummary()

	metrics, err := k8sSummaryProvider.getContainerMetrics(&summary.Pods[0])
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
}

// TestGetNodeMetrics verify tags on node level metrics.
func TestGetNodeMetrics(t *testing.T) {
	hostInfo := mockKubeletDependencies()

	k8sSummaryProvider, err := new(&zap.Logger{}, hostInfo, createKubeletDecoratorWithMockKubeletProvider(t, &zap.Logger{}))
	summary, err := k8sSummaryProvider.kubeletProvider.GetSummary()

	metrics, err := k8sSummaryProvider.getNodeMetrics(summary)
	assert.NoError(t, err)
	assert.NotNil(t, metrics)

	containerMetric := metrics[1]
	assert.Equal(t, containerMetric.GetMetricType(), ci.TypeNode)
}
