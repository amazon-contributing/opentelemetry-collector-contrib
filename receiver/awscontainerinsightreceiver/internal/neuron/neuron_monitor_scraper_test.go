package nueron

import (
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/prometheusscraper"
	"github.com/stretchr/testify/assert"
)

const renameMetric = `
# HELP python_gc_objects_collected_total Objects collected during gc
# TYPE python_gc_objects_collected_total counter
python_gc_objects_collected_total{generation="0"} 75.0
# HELP execution_errors_created Execution errors total
# TYPE execution_errors_created gauge
execution_errors_created{availability_zone="us-east-1c",error_type="generic",instance_id="i-09db9b55e0095612f",instance_name="",instance_type="trn1n.32xlarge",region="us-east-1",runtime_tag="367",subnet_id="subnet-06a7754948e8a000f"} 1.7083389404380567e+09
# HELP neuron_runtime_memory_used_bytes Runtime memory used bytes
# TYPE neuron_runtime_memory_used_bytes gauge
neuron_runtime_memory_used_bytes{availability_zone="us-east-1c",instance_id="i-09db9b55e0095612f",instance_name="",instance_type="trn1n.32xlarge",memory_location="host",region="us-east-1",runtime_tag="367",subnet_id="subnet-06a7754948e8a000f"} 9.043968e+06
# HELP neuroncore_utilization_ratio NeuronCore utilization ratio
# TYPE neuroncore_utilization_ratio gauge
neuroncore_utilization_ratio{availability_zone="us-east-1c",instance_id="i-09db9b55e0095612f",instance_name="",instance_type="trn1n.32xlarge",neuroncore="0",region="us-east-1",runtime_tag="367",subnet_id="subnet-06a7754948e8a000f"} 0.1
# HELP system_memory_total_bytes System memory total_bytes bytes
# TYPE system_memory_total_bytes gauge
system_memory_total_bytes{availability_zone="us-east-1c",instance_id="i-09db9b55e0095612f",instance_name="",instance_type="trn1n.32xlarge",region="us-east-1",subnet_id="subnet-06a7754948e8a000f"} 5.32523487232e+011
`

const dummyClusterName = "cluster-name"
const dummyHostName = "i-000000000"

type mockHostInfoProvider struct {
}

func (m mockHostInfoProvider) GetClusterName() string {
	return dummyClusterName
}

func (m mockHostInfoProvider) GetInstanceID() string {
	return dummyHostName
}

func TestNewNeuronScraperEndToEnd(t *testing.T) {
	expectedMetrics := make(map[string]prometheusscraper.ExpectedMetricStruct)
	expectedMetrics["neuroncore_utilization_ratio"] = prometheusscraper.ExpectedMetricStruct{
		MetricValue: 0.1,
		MetricLabels: []prometheusscraper.MetricLabel{
			{LabelName: "InstanceId", LabelValue: "i-09db9b55e0095612f"},
			{LabelName: "ClusterName", LabelValue: dummyClusterName},
			{LabelName: "DeviceId", LabelValue: "0"},
		},
	}
	expectedMetrics["neuron_runtime_memory_used_bytes"] = prometheusscraper.ExpectedMetricStruct{
		MetricValue: 9.043968e+06,
		MetricLabels: []prometheusscraper.MetricLabel{
			{LabelName: "InstanceId", LabelValue: "i-09db9b55e0095612f"},
			{LabelName: "ClusterName", LabelValue: dummyClusterName},
		},
	}

	expectedMetrics["execution_errors_created"] = prometheusscraper.ExpectedMetricStruct{
		MetricValue: 1.7083389404380567e+09,
		MetricLabels: []prometheusscraper.MetricLabel{
			{LabelName: "InstanceId", LabelValue: "i-09db9b55e0095612f"},
			{LabelName: "ClusterName", LabelValue: dummyClusterName},
		},
	}

	expectedMetrics["system_memory_total_bytes"] = prometheusscraper.ExpectedMetricStruct{
		MetricValue: 5.32523487232e+011,
		MetricLabels: []prometheusscraper.MetricLabel{
			{LabelName: "InstanceId", LabelValue: "i-09db9b55e0095612f"},
			{LabelName: "ClusterName", LabelValue: dummyClusterName},
		},
	}

	expectedMetrics["up"] = prometheusscraper.ExpectedMetricStruct{
		MetricValue:  1.0,
		MetricLabels: []prometheusscraper.MetricLabel{},
	}

	consumer := prometheusscraper.MockConsumer{
		T:               t,
		ExpectedMetrics: expectedMetrics,
	}

	mockedScraperOpts := prometheusscraper.GetMockedScraperOpts(consumer, mockHostInfoProvider{})

	prometheusscraper.TestSimplePrometheusEndToEnd(prometheusscraper.TestSimplePrometheusEndToEndOpts{
		T:                   t,
		Consumer:            consumer,
		DataReturned:        renameMetric,
		ScraperOpts:         mockedScraperOpts,
		ScrapeConfig:        GetNueronScrapeConfig(mockedScraperOpts),
		MetricRelabelConfig: GetNueronMetricRelabelConfigs(mockedScraperOpts),
	})
}

func TestNeuronMonitorScraperJobName(t *testing.T) {
	// needs to start with containerInsights
	assert.True(t, strings.HasPrefix(jobName, "containerInsightsNeuronMonitorScraper"))
}
