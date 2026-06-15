// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusscraper // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/prometheusscraper"

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	configutil "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/mocks"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"
)

type MetricLabel struct {
	LabelName  string
	LabelValue string
}

type ExpectedMetricStruct struct {
	MetricValue  float64
	MetricLabels []MetricLabel
}

// IsFailedOrStaleScrape reports whether the metrics batch corresponds to a
// failed or stale scrape (mock prometheus 404 follow-up). Indicators are an
// `up` metric with value 0, or every series carrying a staleness-marker
// datapoint (NumberDataPointValueTypeEmpty).
//
// Test mockConsumers should skip such batches because they carry no real
// data and would otherwise trip per-call value/label assertions. Using a
// content-based check (this helper) instead of a one-shot first-call latch
// preserves the ability to surface a regressed second successful scrape.
func IsFailedOrStaleScrape(scopeMetrics pmetric.MetricSlice) bool {
	allStale := scopeMetrics.Len() > 0
	for i := 0; i < scopeMetrics.Len(); i++ {
		metric := scopeMetrics.At(i)
		if metric.Type() != pmetric.MetricTypeGauge || metric.Gauge().DataPoints().Len() == 0 {
			allStale = false
			continue
		}
		dp := metric.Gauge().DataPoints().At(0)
		if metric.Name() == "up" && dp.ValueType() == pmetric.NumberDataPointValueTypeDouble && dp.DoubleValue() == 0 {
			return true
		}
		if dp.ValueType() != pmetric.NumberDataPointValueTypeEmpty {
			allStale = false
		}
	}
	return allStale
}

type TestSimplePrometheusEndToEndOpts struct {
	T                   *testing.T
	Consumer            consumer.Metrics
	DataReturned        string
	ScraperOpts         SimplePrometheusScraperOpts
	MetricRelabelConfig []*relabel.Config
}

type MockConsumer struct {
	T                *testing.T
	ExpectedMetrics  map[string]ExpectedMetricStruct
	AdditionalLabels []string
}

func (MockConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{
		MutatesData: false,
	}
}

func (m MockConsumer) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	expectedMetricsCount := len(m.ExpectedMetrics)
	metricFoundCount := 0

	assert.Equal(m.T, 1, md.ResourceMetrics().Len())

	scopeMetrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	// The mock prometheus server returns one valid page and a 404 on the
	// follow-up scrape; the 404 path produces an `up=0` synthetic metric and
	// staleness markers for every prior series. Skip such calls so they do
	// not trip the value/label assertions below — they carry no real data.
	if IsFailedOrStaleScrape(scopeMetrics) {
		return nil
	}
	for i := 0; i < scopeMetrics.Len(); i++ {
		metric := scopeMetrics.At(i)
		metricsStruct, ok := m.ExpectedMetrics[metric.Name()]
		if ok {
			assert.Equal(m.T, metricsStruct.MetricValue, metric.Gauge().DataPoints().At(0).DoubleValue())
			for _, expectedLabel := range metricsStruct.MetricLabels {
				labelValue, isFound := metric.Gauge().DataPoints().At(0).Attributes().Get(expectedLabel.LabelName)
				assert.True(m.T, isFound)
				assert.Equal(m.T, expectedLabel.LabelValue, labelValue.Str())
			}
			for _, specialLabel := range m.AdditionalLabels {
				isLabelInExpected := slices.ContainsFunc(metricsStruct.MetricLabels, func(label MetricLabel) bool { return label.LabelName == specialLabel })
				_, isLabelInActual := metric.Gauge().DataPoints().At(0).Attributes().Get(specialLabel)
				assert.Equal(m.T, isLabelInExpected, isLabelInActual)
			}
			metricFoundCount++
		}
	}

	assert.Equal(m.T, expectedMetricsCount, metricFoundCount)

	return nil
}

func TestSimplePrometheusEndToEnd(opts TestSimplePrometheusEndToEndOpts) {
	scraper, err := NewSimplePrometheusScraper(opts.ScraperOpts)
	assert.NoError(opts.T, err)

	// build up a new PR
	promFactory := prometheusreceiver.NewFactory()

	targets := []*mocks.TestData{
		{
			Name: "neuron",
			Pages: []mocks.MockPrometheusResponse{
				{Code: 200, Data: opts.DataReturned},
			},
		},
	}
	mp, cfg, err := mocks.SetupMockPrometheus(targets...)
	assert.NoError(opts.T, err)

	split := strings.Split(mp.Srv.URL, "http://")

	mockedScrapeConfig := &config.ScrapeConfig{
		ScrapeProtocols: config.DefaultScrapeProtocols,
		HTTPClientConfig: configutil.HTTPClientConfig{
			TLSConfig: configutil.TLSConfig{
				InsecureSkipVerify: true,
			},
		},
		ScrapeInterval:  cfg.ScrapeConfigs[0].ScrapeInterval,
		ScrapeTimeout:   cfg.ScrapeConfigs[0].ScrapeInterval,
		JobName:         fmt.Sprintf("%s/%s", "jobName", cfg.ScrapeConfigs[0].MetricsPath),
		HonorTimestamps: true,
		Scheme:          "http",
		MetricsPath:     cfg.ScrapeConfigs[0].MetricsPath,
		ServiceDiscoveryConfigs: discovery.Configs{
			// using dummy static config to avoid service discovery initialization
			discovery.StaticConfig{
				{
					Targets: []model.LabelSet{
						{
							model.AddressLabel: model.LabelValue(split[1]),
						},
					},
				},
			},
		},
		RelabelConfigs:       []*relabel.Config{},
		MetricRelabelConfigs: opts.MetricRelabelConfig,
	}

	promConfig := prometheusreceiver.Config{
		PrometheusConfig: &prometheusreceiver.PromConfig{
			ScrapeConfigs: []*config.ScrapeConfig{mockedScrapeConfig},
		},
	}

	// replace the prom receiver
	params := receiver.Settings{
		TelemetrySettings: scraper.Settings,
		ID:                component.NewID(component.MustNewType("prometheus")),
	}
	scraper.PrometheusReceiver, err = promFactory.CreateMetrics(scraper.Ctx, params, &promConfig, opts.Consumer)
	assert.NoError(opts.T, err)
	assert.NotNil(opts.T, mp)
	defer mp.Close()

	// perform a single scrape, this will kick off the scraper process for additional scrapes
	scraper.GetMetrics()

	opts.T.Cleanup(func() {
		scraper.Shutdown()
	})

	// wait for 2 scrapes, one initiated by us, another by the new scraper process
	mp.Wg.Wait()
	mp.Wg.Wait()
}
