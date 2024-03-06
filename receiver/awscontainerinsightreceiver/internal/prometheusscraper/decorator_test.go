// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusscraper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"
)

var _ Decorator = (*MockK8sDecorator)(nil)

type MockK8sDecorator struct {
}

func (m *MockK8sDecorator) Decorate(metric stores.CIMetric) stores.CIMetric {
	return metric
}

func (m *MockK8sDecorator) Shutdown() error {
	return nil
}

const (
	util      = "UTIL"
	memUtil   = "USED_PERCENT"
	memUsed   = "FB_USED"
	memTotal  = "FB_TOTAL"
	temp      = "TEMP"
	powerDraw = "POWER_USAGE"
)

var metricToUnit = map[string]string{
	util:      "Percent",
	memUtil:   "Percent",
	memUsed:   "Bytes",
	memTotal:  "Bytes",
	temp:      "None",
	powerDraw: "None",
}

func TestConsumeMetricsForGpu(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	dc := &DecorateConsumer{
		ContainerOrchestrator: "EKS",
		NextConsumer:          consumertest.NewNop(),
		K8sDecorator:          &MockK8sDecorator{},
		MetricToUnitMap:       metricToUnit,
		Logger:                logger,
	}
	ctx := context.Background()

	testcases := map[string]struct {
		metrics     pmetric.Metrics
		want        pmetric.Metrics
		shouldError bool
	}{
		"empty": {
			metrics:     pmetric.NewMetrics(),
			want:        pmetric.NewMetrics(),
			shouldError: false,
		},
		"unit": {
			metrics: generateMetrics(map[string]map[string]string{
				util: {
					"device": "test0",
				},
				memUtil: {
					"device": "test0",
				},
				memTotal: {
					"device": "test0",
				},
				memUsed: {
					"device": "test0",
				},
				powerDraw: {
					"device": "test0",
				},
				temp: {
					"device": "test0",
				},
			}),
			want: generateMetrics(map[string]map[string]string{
				util: {
					"device": "test0",
					"Unit":   "Percent",
				},
				memUtil: {
					"device": "test0",
					"Unit":   "Percent",
				},
				memTotal: {
					"device": "test0",
					"Unit":   "Bytes",
				},
				memUsed: {
					"device": "test0",
					"Unit":   "Bytes",
				},
				powerDraw: {
					"device": "test0",
					"Unit":   "None",
				},
				temp: {
					"device": "test0",
					"Unit":   "None",
				},
			}),
			shouldError: false,
		},
		"noUnit": {
			metrics: generateMetrics(map[string]map[string]string{
				"test": {
					"device": "test0",
				},
			}),
			want: generateMetrics(map[string]map[string]string{
				"test": {
					"device": "test0",
				},
			}),
			shouldError: false,
		},
		"typeUnchanged": {
			metrics: generateMetrics(map[string]map[string]string{
				util: {
					"device": "test0",
					"Type":   "TestType",
				},
			}),
			want: generateMetrics(map[string]map[string]string{
				util: {
					"device": "test0",
					"Type":   "TestType",
					"Unit":   "Percent",
				},
			}),
			shouldError: false,
		},
	}

	for _, tc := range testcases {
		err := dc.ConsumeMetrics(ctx, tc.metrics)
		if tc.shouldError {
			assert.Error(t, err)
			return
		}
		require.NoError(t, err)
		assert.Equal(t, tc.want.MetricCount(), tc.metrics.MetricCount())
		if tc.want.MetricCount() == 0 {
			continue
		}
		actuals := tc.metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
		actuals.Sort(func(a, b pmetric.Metric) bool {
			return a.Name() < b.Name()
		})
		wants := tc.want.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
		wants.Sort(func(a, b pmetric.Metric) bool {
			return a.Name() < b.Name()
		})
		for i := 0; i < wants.Len(); i++ {
			actual := actuals.At(i)
			want := wants.At(i)
			assert.Equal(t, want.Name(), actual.Name())
			assert.Equal(t, want.Unit(), actual.Unit())
			actualAttrs := actual.Gauge().DataPoints().At(0).Attributes()
			wantAttrs := want.Gauge().DataPoints().At(0).Attributes()
			assert.Equal(t, wantAttrs.Len(), actualAttrs.Len())
			wantAttrs.Range(func(k string, v pcommon.Value) bool {
				av, ok := actualAttrs.Get(k)
				assert.True(t, ok)
				assert.Equal(t, v, av)
				return true
			})
		}
	}
}

func generateMetrics(nameToDims map[string]map[string]string) pmetric.Metrics {
	md := pmetric.NewMetrics()
	ms := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics()
	for name, dims := range nameToDims {
		m := ms.AppendEmpty()
		m.SetName(name)
		gauge := m.SetEmptyGauge().DataPoints().AppendEmpty()
		gauge.SetIntValue(10)
		for k, v := range dims {
			if k == "Unit" {
				m.SetUnit(v)
				continue
			}
			gauge.Attributes().PutStr(k, v)
		}
	}
	return md
}
