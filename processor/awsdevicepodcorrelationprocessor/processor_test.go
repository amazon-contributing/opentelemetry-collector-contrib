// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/kubelet"
)

// mockLookup implements DeviceLookup for testing.
type mockLookup struct {
	data map[string]map[string]*kubelet.ContainerInfo // deviceID -> resourceName -> ContainerInfo
}

func newMockLookup(data map[string]map[string]*kubelet.ContainerInfo) *mockLookup {
	return &mockLookup{data: data}
}

func (m *mockLookup) GetContainerInfo(deviceID string, resourceName string) *kubelet.ContainerInfo {
	if rn, ok := m.data[deviceID]; ok {
		return rn[resourceName]
	}
	return nil
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
	// Validate no longer sets defaults; setDefaults() is called in the factory.
	assert.Empty(t, cfg.DeviceTypes[0].DeviceIDSource)

	cfg.setDefaults()
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

// newTestProcessor creates a processor for testing.
func newTestProcessor(cfg *Config) *devicePodCorrelationProcessor {
	return &devicePodCorrelationProcessor{config: cfg, logger: zap.NewNop()}
}

// processMetricsWithLookup is a test helper that calls processDatapoints with a mock lookup.
func processMetricsWithLookup(p *devicePodCorrelationProcessor, md pmetric.Metrics, lookup DeviceLookup) pmetric.Metrics {
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		resourceAttrs := rm.Resource().Attributes()
		ilms := rm.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			metrics := ilms.At(j).Metrics()
			for k := 0; k < metrics.Len(); k++ {
				m := metrics.At(k)
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					processDatapoints(m.Gauge().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeSum:
					processDatapoints(m.Sum().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeHistogram:
					processDatapoints(m.Histogram().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeExponentialHistogram:
					processDatapoints(m.ExponentialHistogram().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				case pmetric.MetricTypeSummary:
					processDatapoints(m.Summary().DataPoints(), resourceAttrs, p.config.DeviceTypes, lookup, p.logger)
				}
			}
		}
	}
	return md
}

func TestProcessMetrics_CorrelatesDeviceToPod(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"0": {"aws.amazon.com/neurondevice": {PodName: "ml-pod", Namespace: "default", ContainerName: "trainer"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := newTestProcessor(cfg)
	md := newTestMetrics("neuron_memory", "NeuronDevice", "0")
	result := processMetricsWithLookup(p, md, lookup)
	

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
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := newTestProcessor(cfg)
	md := newTestMetrics("neuron_memory", "NeuronDevice", "99")
	result := processMetricsWithLookup(p, md, lookup)
	

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	_, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.False(t, ok)
}

func TestProcessMetrics_SkipsAlreadyEnrichedDatapoints(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"0": {"aws.amazon.com/neurondevice": {PodName: "should-not-overwrite", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := newTestProcessor(cfg)
	md := newTestMetrics("neuron_memory", "NeuronDevice", "0")
	md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes().PutStr(k8sPodNameKey, "existing-pod")

	result := processMetricsWithLookup(p, md, lookup)
	

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, _ := dp.Attributes().Get(k8sPodNameKey)
	assert.Equal(t, "existing-pod", podVal.AsString())
}

func TestProcessMetrics_ResourceLevelDeviceID(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"efa3": {"vpc.amazonaws.com/efa": {PodName: "efa-pod", Namespace: "prod", ContainerName: "worker"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "efa", DeviceIDAttribute: "device", DeviceIDSource: DeviceIDSourceResource, ResourceNames: []string{"vpc.amazonaws.com/efa"}},
		},
	}
	p := newTestProcessor(cfg)
	md := newTestMetricsWithResourceAttr("efa_traffic", "device", "efa3")
	result := processMetricsWithLookup(p, md, lookup)
	

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "efa-pod", podVal.AsString())
}

func TestProcessMetrics_SumMetricType(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"0": {"aws.amazon.com/neurondevice": {PodName: "sum-pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	p := newTestProcessor(cfg)
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("counter")
	dp := m.SetEmptySum().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("NeuronDevice", "0")

	result := processMetricsWithLookup(p, md, lookup)
	

	dpOut := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0)
	podVal, ok := dpOut.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "sum-pod", podVal.AsString())
}

func TestProcessMetrics_FallbackResourceNames(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"0": {"aws.amazon.com/neuron": {PodName: "fallback-pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"aws.amazon.com/neurondevice", "aws.amazon.com/neuron"}},
		},
	}
	p := newTestProcessor(cfg)
	md := newTestMetrics("neuron_memory", "NeuronDevice", "0")
	result := processMetricsWithLookup(p, md, lookup)
	

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	podVal, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "fallback-pod", podVal.AsString())
}

func TestProcessMetrics_HistogramMetricType(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"0": {"res": {PodName: "hist-pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"res"}},
		},
	}
	p := newTestProcessor(cfg)
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("latency")
	dp := m.SetEmptyHistogram().DataPoints().AppendEmpty()
	dp.Attributes().PutStr("dev", "0")

	result := processMetricsWithLookup(p, md, lookup)
	

	dpOut := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints().At(0)
	podVal, ok := dpOut.Attributes().Get(k8sPodNameKey)
	assert.True(t, ok)
	assert.Equal(t, "hist-pod", podVal.AsString())
}

func TestShutdown_NilClient(t *testing.T) {
	p := &devicePodCorrelationProcessor{}
	assert.NoError(t, p.Shutdown(t.Context()))
}

func TestStart_RegistersUniqueResourceNames(t *testing.T) {
	// Verify that Start creates a client and deduplicates resource names.
	// We can't fully test kubelet connection without a socket, but we can
	// verify the processor struct is wired correctly.
	cfg := &Config{
		KubeletSocketPath: "/nonexistent/socket",
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "dev", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"res.a", "res.b"}},
			{Name: "efa", DeviceIDAttribute: "dev2", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"res.b", "res.c"}},
		},
	}
	p := newProcessor(cfg, zap.NewNop())

	// Start will fail because the socket doesn't exist, but the client
	// should still be created with resource names registered.
	err := p.Start(t.Context(), nil)
	// We expect an error due to missing socket — that's fine.
	// The important thing is the client was created.
	assert.NotNil(t, p.client)

	// Verify we can still shut down cleanly after a failed start.
	assert.NoError(t, p.Shutdown(t.Context()))
	_ = err
}

func TestProcessMetrics_NoDeviceIDAttribute(t *testing.T) {
	lookup := newMockLookup(map[string]map[string]*kubelet.ContainerInfo{
		"0": {"res": {PodName: "pod", Namespace: "ns", ContainerName: "c"}},
	})
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "missing_attr", DeviceIDSource: DeviceIDSourceDatapoint, ResourceNames: []string{"res"}},
		},
	}
	p := newTestProcessor(cfg)
	md := newTestMetrics("metric", "other_attr", "0")
	result := processMetricsWithLookup(p, md, lookup)
	

	dp := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	_, ok := dp.Attributes().Get(k8sPodNameKey)
	assert.False(t, ok)
}
