// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdeviceslurmjobcorrelationprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap/zaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/slurm"
)

// mockResolverForTest is a Resolver-compatible mock that returns predefined job info.
type mockResolverForTest struct {
	mappings map[string]*slurm.JobInfo
}

func (m *mockResolverForTest) GetJobInfo(deviceID string, deviceType string) *slurm.JobInfo {
	key := deviceType + ":" + deviceID
	return m.mappings[key]
}

func newTestProcessorWithMock(t *testing.T, cfg *Config, mock *mockResolverForTest) *slurmJobCorrelationProcessor {
	t.Helper()
	p := newProcessor(cfg, zaptest.NewLogger(t))
	p.mockLookup = mock
	return p
}

func TestProcessMetrics_GPUCorrelation(t *testing.T) {
	mock := &mockResolverForTest{
		mappings: map[string]*slurm.JobInfo{
			"gpu:0": {JobID: "12345", JobName: "training-llm", User: "miconeil", Account: "ml-team", Partition: "gpu-queue"},
			"gpu:1": {JobID: "12346", JobName: "inference", User: "alice", Account: "inference-team", Partition: "gpu-queue"},
		},
	}

	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "gpu", DeviceIDSource: DeviceIDSourceDatapoint},
		},
	}
	p := newTestProcessorWithMock(t, cfg, mock)

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("DCGM_FI_DEV_GPU_UTIL")
	gauge := m.SetEmptyGauge()

	dp := gauge.DataPoints().AppendEmpty()
	dp.SetDoubleValue(85.0)
	dp.Attributes().PutStr("gpu", "0")

	dp2 := gauge.DataPoints().AppendEmpty()
	dp2.SetDoubleValue(42.0)
	dp2.Attributes().PutStr("gpu", "1")

	result, err := p.processMetrics(context.Background(), md)
	require.NoError(t, err)

	attrs0 := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes()
	jobID, ok := attrs0.Get(slurmJobIDKey)
	require.True(t, ok)
	assert.Equal(t, "12345", jobID.Str())

	jobName, ok := attrs0.Get(slurmJobNameKey)
	require.True(t, ok)
	assert.Equal(t, "training-llm", jobName.Str())

	user, ok := attrs0.Get(slurmJobUserKey)
	require.True(t, ok)
	assert.Equal(t, "miconeil", user.Str())

	account, ok := attrs0.Get(slurmJobAccountKey)
	require.True(t, ok)
	assert.Equal(t, "ml-team", account.Str())

	partition, ok := attrs0.Get(slurmJobPartitionKey)
	require.True(t, ok)
	assert.Equal(t, "gpu-queue", partition.Str())

	// Second datapoint
	attrs1 := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(1).Attributes()
	jobID1, ok := attrs1.Get(slurmJobIDKey)
	require.True(t, ok)
	assert.Equal(t, "12346", jobID1.Str())
}

func TestProcessMetrics_EFACorrelation_ResourceAttribute(t *testing.T) {
	mock := &mockResolverForTest{
		mappings: map[string]*slurm.JobInfo{
			"efa:efa_0": {JobID: "99", JobName: "nccl-test", User: "bob", Partition: "gpu-queue"},
		},
	}

	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "efa", DeviceIDAttribute: "aws.efa.device", DeviceIDSource: DeviceIDSourceResource},
		},
	}
	p := newTestProcessorWithMock(t, cfg, mock)

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("aws.efa.device", "efa_0")
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("efa_rdma_write_bytes")
	sum := m.SetEmptySum()
	dp := sum.DataPoints().AppendEmpty()
	dp.SetIntValue(1024000)

	result, err := p.processMetrics(context.Background(), md)
	require.NoError(t, err)

	attrs := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0).Attributes()
	jobID, ok := attrs.Get(slurmJobIDKey)
	require.True(t, ok)
	assert.Equal(t, "99", jobID.Str())

	jobName, ok := attrs.Get(slurmJobNameKey)
	require.True(t, ok)
	assert.Equal(t, "nccl-test", jobName.Str())
}

func TestProcessMetrics_NoMatch(t *testing.T) {
	mock := &mockResolverForTest{mappings: map[string]*slurm.JobInfo{}}

	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "gpu", DeviceIDSource: DeviceIDSourceDatapoint},
		},
	}
	p := newTestProcessorWithMock(t, cfg, mock)

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("DCGM_FI_DEV_GPU_UTIL")
	gauge := m.SetEmptyGauge()
	dp := gauge.DataPoints().AppendEmpty()
	dp.SetDoubleValue(10.0)
	dp.Attributes().PutStr("gpu", "7")

	result, err := p.processMetrics(context.Background(), md)
	require.NoError(t, err)

	_, ok := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes().Get(slurmJobIDKey)
	assert.False(t, ok)
}

func TestProcessMetrics_AlreadyAnnotated(t *testing.T) {
	mock := &mockResolverForTest{
		mappings: map[string]*slurm.JobInfo{
			"gpu:0": {JobID: "999", JobName: "override-attempt"},
		},
	}

	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "gpu", DeviceIDSource: DeviceIDSourceDatapoint},
		},
	}
	p := newTestProcessorWithMock(t, cfg, mock)

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("DCGM_FI_DEV_GPU_UTIL")
	gauge := m.SetEmptyGauge()
	dp := gauge.DataPoints().AppendEmpty()
	dp.SetDoubleValue(50.0)
	dp.Attributes().PutStr("gpu", "0")
	dp.Attributes().PutStr(slurmJobIDKey, "existing-job")

	result, err := p.processMetrics(context.Background(), md)
	require.NoError(t, err)

	jobID, _ := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes().Get(slurmJobIDKey)
	assert.Equal(t, "existing-job", jobID.Str())
}

func TestProcessMetrics_SumMetricType(t *testing.T) {
	mock := &mockResolverForTest{
		mappings: map[string]*slurm.JobInfo{
			"gpu:0": {JobID: "500", JobName: "counter-job"},
		},
	}

	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "gpu", DeviceIDSource: DeviceIDSourceDatapoint},
		},
	}
	p := newTestProcessorWithMock(t, cfg, mock)

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	m := sm.Metrics().AppendEmpty()
	m.SetName("DCGM_FI_DEV_TOTAL_ENERGY_CONSUMPTION")
	sum := m.SetEmptySum()
	dp := sum.DataPoints().AppendEmpty()
	dp.SetIntValue(123456789)
	dp.Attributes().PutStr("gpu", "0")

	result, err := p.processMetrics(context.Background(), md)
	require.NoError(t, err)

	attrs := result.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0).Attributes()
	jobID, ok := attrs.Get(slurmJobIDKey)
	require.True(t, ok)
	assert.Equal(t, "500", jobID.Str())
}

func TestFactory(t *testing.T) {
	factory := NewFactory()
	assert.Equal(t, "awsdevicejobcorrelation", factory.Type().String())

	cfg := factory.CreateDefaultConfig()
	require.NotNil(t, cfg)

	processorCfg, ok := cfg.(*Config)
	require.True(t, ok)
	assert.Equal(t, "/sys/fs/cgroup", processorCfg.CgroupRoot)
	assert.Equal(t, true, processorCfg.NodeExclusive)
	assert.Len(t, processorCfg.DeviceTypes, 2)
}
