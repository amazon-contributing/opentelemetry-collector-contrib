// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package keda
import (
	"context"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const (
	scalerType    = "scaler_type"
	scaledObject  = "scaled_object"
	errorType     = "error_type"
	DefaultValue  = "DEFAULT"
)

var attributeConfig = map[string][]string{
	KedaScalerActive:        {scalerType, scaledObject},
	KedaScalerErrors:        {scalerType, scaledObject, errorType},
	KedaScaledObjectErrors:  {scaledObject, errorType},
}

var defaultAttributeValues = map[string]string{
	scalerType:   "prometheus",
	scaledObject: "default",
	errorType:    "generic",
}

type EmptyMetricDecorator struct {
	NextConsumer consumer.Metrics
	Logger       *zap.Logger
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
		ilms := rs.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			ils := ilms.At(j)
			metrics := ils.Metrics()
			ed.addEmptyMetrics(metrics)
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