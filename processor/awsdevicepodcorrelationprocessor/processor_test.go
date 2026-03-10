// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// mockStore implements PodResourcesStoreInterface for testing.
type mockStore struct {
	data          map[string]map[string]*ContainerInfo // deviceID -> resourceName -> ContainerInfo
	resourceNames []string
	shutdownCalled bool
}

func newMockStore(data map[string]map[string]*ContainerInfo) *mockStore {
	return &mockStore{data: data}
}

func (m *mockStore) GetContainerInfo(deviceID string, resourceName string) *ContainerInfo {
	if rn, ok := m.data[deviceID]; ok {
		return rn[resourceName]
	}
	return nil
}

func (m *mockStore) AddResourceName(resourceName string) {
	m.resourceNames = append(m.resourceNames, resourceName)
}

func (m *mockStore) Shutdown() {
	m.shutdownCalled = true
}

// --- Config validation tests ---

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_EmptyDeviceTypes(t *testing.T) {
	cfg := &Config{}
	assert.ErrorContains(t, cfg.Validate(), "device_types must not be empty")
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{DeviceIDAttribute: "dev", ResourceNames: []string{"res"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "name must not be empty")
}

func TestValidate_MissingDeviceIDAttribute(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", ResourceNames: []string{"res"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "device_id_attribute must not be empty")
}

func TestValidate_MissingResourceNames(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev"},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "resource_names must not be empty")
}

func TestValidate_InvalidDeviceIDSource(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", DeviceIDSource: "invalid", ResourceNames: []string{"res"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "device_id_source must be")
}

func TestValidate_DuplicateName(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev1", ResourceNames: []string{"res1"}},
			{Name: "gpu", DeviceIDAttribute: "dev2", ResourceNames: []string{"res2"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "duplicate name")
}

func TestValidate_DefaultsDeviceIDSource(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", ResourceNames: []string{"res"}},
		},
	}
	require.NoError(t, cfg.Validate())
	assert.Equal(t, DeviceIDSourceDatapoint, cfg.DeviceTypes[0].DeviceIDSource)
}

// --- processMetrics tests ---

func newTestMetrics(metricName string, deviceIDKey string, deviceIDVal string) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(metricName)
	dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
	dp.Attributes().PutStr(deviceIDKey, deviceIDVal)
	return md
}

func newTestMetricsWithResourceAttr(metricName string, resourceKey string, resourceVal string) pmetric.Metrics {
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr(resourceKey, resourceVal)
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(metricName)
	m.SetEmptyGauge().DataPoints().AppendEmpty()
	return md
}

func TestProcessMetrics_CorrelatesDeviceToPod(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"0": {"aws.amazon.com/neurondevice": {PodName: "ml-pod", Namespace: "default", ContainerName: "trainer"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := newTestMetrics("neuron_memory", "NeuronDevice", "0")
	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "ml-pod", podVal.AsString())
	nsVal, _ := dp.Attributes().Get(k8sNamespaceKey)
	assert.Equal(t, "default", nsVal.AsString())
	cVal, _ := dp.Attributes().Get(containerNameKey)
	assert.Equal(t, "trainer", cVal.AsString())
}

func TestProcessMetrics_NoMatchLeavesDatapointUnchanged(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := newTestMetrics("neuron_memory", "NeuronDevice", "99")
	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	_, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.False(t, ok)
}

func TestProcessMetrics_SkipsAlreadyEnrichedDatapoints(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"0": {"aws.amazon.com/neurondevice": {PodName: "should-not-overwrite", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := newTestMetrics("neuron_memory", "NeuronDevice", "0")
	// Pre-set pod name
	md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes().PutStr(k8sPodNameKey, "existing-pod")

	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, _ := dp.Attributes().Get(k8sPodNameKey)
	assert.Equal(t, "existing-pod", podVal.AsString())
}

func TestProcessMetrics_ResourceLevelDeviceID(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"efa3": {"vpc.amazonaws.com/efa": {PodName: "efa-pod", Namespace: "prod", ContainerName: "worker"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "efa", DeviceIDAttribute: "device", DeviceIDSource: DeviceIDSourceResource, ResourceNames: []string{"vpc.amazonaws.com/efa"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := newTestMetricsWithResourceAttr("efa_traffic", "device", "efa3")
	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "efa-pod", podVal.AsString())
}

func TestProcessMetrics_SumMetricType(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"0": {"aws.amazon.com/neurondevice": {PodName: "sum-pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("counter")
	dp := m.SetEmptySum().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("NeuronDevice", "0")

	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dpOut := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
	podVal, ok := dpOut.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "sum-pod", podVal.AsString())
}

func TestProcessMetrics_FallbackResourceNames(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"0": {"aws.amazon.com/neuron": {PodName: "fallback-pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice", "aws.amazon.com/neuron"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := newTestMetrics("neuron_memory", "NeuronDevice", "0")
	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "fallback-pod", podVal.AsString())
}

// --- processDatapoints generic test with histogram ---

func TestProcessMetrics_HistogramMetricType(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"0": {"res": {PodName: "hist-pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"res"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency")
	dp := m.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("dev", "0")

	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dpOut := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints().At(0)
	podVal, ok := dpOut.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "hist-pod", podVal.AsString())
}

// --- Shutdown test ---

func TestShutdown_CallsStoreShutdown(t *testing.T) {
	store := newMockStore(nil)
	p := &devicePodCorrelationProcessor{podResourcesStore: store}
	_ = p.Shutdown(nil)
	assert.True(t, store.shutdownCalled)
}

func TestShutdown_NilStore(t *testing.T) {
	p := &devicePodCorrelationProcessor{}
	assert.NoError(t, p.Shutdown(nil))
}

// --- processDatapoints with no matching device ID attribute ---

func TestProcessMetrics_NoDeviceIDAttribute(t *testing.T) {
	store := newMockStore(map[string]map[string]*ContainerInfo{
		"0": {"res": {PodName: "pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "missing_attr", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"res"}},
		},
	}
	p := &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop(), podResourcesStore: store}

	md := newTestMetrics("metric", "other_attr", "0")
	result, err := p.processMetrics(nil, md)
	require.NoError(t, err)

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	_, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.False(t, ok)
}
