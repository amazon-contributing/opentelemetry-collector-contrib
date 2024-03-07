// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package neuron

import (
	"context"
	"testing"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/prometheusscraper/decoratorconsumer"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

var dummyPodName = "pod-name"
var dummyContainerName = "container-name"
var dummyNamespace = "namespace"

type mockPodResourcesStore struct {
}

func (m mockPodResourcesStore) GetContainerInfo(deviceIndex string, resourceName string) *stores.ContainerInfo {
	return &stores.ContainerInfo{
		PodName:       dummyPodName,
		ContainerName: dummyContainerName,
		Namespace:     dummyNamespace,
	}
}

func TestConsumeMetricsForPodAttributeDecorator(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	dc := &PodAttributesDecoratorConsumer{
		NextConsumer:      consumertest.NewNop(),
		PodResourcesStore: mockPodResourcesStore{},
		Logger:            logger,
	}
	ctx := context.Background()

	testcases := map[string]decoratorconsumer.TestCase{
		"empty": {
			Metrics:     pmetric.NewMetrics(),
			Want:        pmetric.NewMetrics(),
			ShouldError: false,
		},
		"neuron_hardware_info_not_found": {
			Metrics: decoratorconsumer.GenerateMetrics(map[decoratorconsumer.MetricIdentifier]map[string]string{
				{Name: "test", MetricType: pmetric.MetricTypeGauge}: {
					"device": "test0",
				},
			}),

			Want: decoratorconsumer.GenerateMetrics(map[decoratorconsumer.MetricIdentifier]map[string]string{
				{Name: "test", MetricType: pmetric.MetricTypeGauge}: {
					"device": "test0",
				},
			}),
			ShouldError: false,
		},
		"correlation_via_neuron_device_index": {
			Metrics: decoratorconsumer.GenerateMetrics(map[decoratorconsumer.MetricIdentifier]map[string]string{
				{Name: neuronHardwareInfoKey, MetricType: pmetric.MetricTypeSum}: {
					neuronCorePerDeviceKey: "2",
				},
				{Name: "test", MetricType: pmetric.MetricTypeGauge}: {
					"device":                 "test0",
					neuronDeviceAttributeKey: "1",
				},
			}),
			Want: decoratorconsumer.GenerateMetrics(map[decoratorconsumer.MetricIdentifier]map[string]string{
				{Name: neuronHardwareInfoKey, MetricType: pmetric.MetricTypeSum}: {
					neuronCorePerDeviceKey: "2",
				},
				{Name: "test", MetricType: pmetric.MetricTypeGauge}: {
					"device":                  "test0",
					neuronDeviceAttributeKey:  "1",
					ci.AttributeContainerName: dummyContainerName,
					ci.AttributeK8sPodName:    dummyPodName,
					ci.AttributePodName:       dummyPodName,
					ci.AttributeK8sNamespace:  dummyNamespace,
				},
			}),
			ShouldError: false,
		},
		"correlation_via_neuron_core": {
			Metrics: decoratorconsumer.GenerateMetrics(map[decoratorconsumer.MetricIdentifier]map[string]string{
				{Name: neuronHardwareInfoKey, MetricType: pmetric.MetricTypeSum}: {
					neuronCorePerDeviceKey: "2",
				},
				{Name: "test", MetricType: pmetric.MetricTypeGauge}: {
					"device":               "test0",
					neuronCoreAttributeKey: "10",
				},
			}),
			Want: decoratorconsumer.GenerateMetrics(map[decoratorconsumer.MetricIdentifier]map[string]string{
				{Name: neuronHardwareInfoKey, MetricType: pmetric.MetricTypeSum}: {
					neuronCorePerDeviceKey: "2",
				},
				{Name: "test", MetricType: pmetric.MetricTypeGauge}: {
					"device":                  "test0",
					neuronCoreAttributeKey:    "10",
					neuronDeviceAttributeKey:  "5",
					ci.AttributeContainerName: dummyContainerName,
					ci.AttributeK8sPodName:    dummyPodName,
					ci.AttributePodName:       dummyPodName,
					ci.AttributeK8sNamespace:  dummyNamespace,
				},
			}),
			ShouldError: false,
		},
	}

	decoratorconsumer.RunDecoratorTestScenarios(t, dc, ctx, testcases)
}
