// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sapiserver

import (
	"context"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/mocks"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

const kueueMetrics = `
# HELP kueue_pending_workloads Number of pending workloads per cluster queue and status
# TYPE kueue_pending_workloads gauge
kueue_pending_workloads{queue="default"} 3
# HELP kueue_admitted_active_workloads The number of admitted workloads that are active per cluster queue
# TYPE kueue_admitted_active_workloads gauge
kueue_admitted_active_workloads{queue="default"} 5
`

type mockKueueConsumer struct {
	t                    *testing.T
	called               *bool
	pendingWorkloadCount *bool
	activeWorkloadCount  *bool
}

func (m mockKueueConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{
		MutatesData: false,
	}
}

func (m mockKueueConsumer) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	assert.Equal(m.t, 1, md.ResourceMetrics().Len())

	scopeMetrics := md.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	for i := 0; i < scopeMetrics.Len(); i++ {
		metric := scopeMetrics.At(i)
		switch metric.Name() {
		case "kueue_pending_workloads":
			assert.Equal(m.t, float64(3), metric.Gauge().DataPoints().At(0).DoubleValue())
			*m.pendingWorkloadCount = true
		case "kueue_admitted_active_workloads":
			assert.Equal(m.t, float64(5), metric.Gauge().DataPoints().At(0).DoubleValue())
			*m.activeWorkloadCount = true
		}
	}
	*m.called = true
	return nil
}

func TestNewKueuePrometheusScraperBadInputs(t *testing.T) {
	settings := componenttest.NewNopTelemetrySettings()
	settings.Logger, _ = zap.NewDevelopment()

	leaderElection := LeaderElection{
		leading: true,
	}

	tests := []KueuePrometheusScraperOpts{
		{ // case: no leader election
			Ctx:                 context.TODO(),
			TelemetrySettings:   settings,
			Consumer:            mockKueueConsumer{},
			Host:                componenttest.NewNopHost(),
			ClusterNameProvider: mockClusterNameProvider{},
			LeaderElection:      nil,
			BearerToken:         "/path/to/dummy/token",
		},
		{ // case: no consumer
			Ctx:                 context.TODO(),
			TelemetrySettings:   settings,
			Consumer:            nil,
			Host:                componenttest.NewNopHost(),
			ClusterNameProvider: mockClusterNameProvider{},
			LeaderElection:      &leaderElection,
			BearerToken:         "/path/to/dummy/token",
		},
		{ // case: no host
			Ctx:                 context.TODO(),
			TelemetrySettings:   settings,
			Consumer:            mockKueueConsumer{},
			Host:                nil,
			ClusterNameProvider: mockClusterNameProvider{},
			LeaderElection:      &leaderElection,
			BearerToken:         "/path/to/dummy/token",
		},
		{ // case: no cluster name provider
			Ctx:                 context.TODO(),
			TelemetrySettings:   settings,
			Consumer:            mockKueueConsumer{},
			Host:                componenttest.NewNopHost(),
			ClusterNameProvider: nil,
			LeaderElection:      &leaderElection,
			BearerToken:         "/path/to/dummy/token",
		},
	}

	for _, tt := range tests {
		scraper, err := NewKueuePrometheusScraper(tt)

		assert.Error(t, err)
		assert.Nil(t, scraper)
	}
}

func TestNewKueuePrometheusScraperEndToEnd(t *testing.T) {
	consumerCalled := false
	pendingWorkloadCount := false
	activeWorkloadCount := false

	mConsumer := mockKueueConsumer{
		t:                    t,
		called:               &consumerCalled,
		pendingWorkloadCount: &pendingWorkloadCount,
		activeWorkloadCount:  &activeWorkloadCount,
	}

	settings := componenttest.NewNopTelemetrySettings()
	settings.Logger, _ = zap.NewDevelopment()

	leaderElection := LeaderElection{
		leading: true,
	}

	scraper, err := NewKueuePrometheusScraper(
		KueuePrometheusScraperOpts{
			Ctx:                 context.TODO(),
			TelemetrySettings:   settings,
			Consumer:            mConsumer,
			Host:                componenttest.NewNopHost(),
			ClusterNameProvider: mockClusterNameProvider{},
			LeaderElection:      &leaderElection,
			BearerToken:         "",
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, mockClusterNameProvider{}, scraper.clusterNameProvider)

	// build up a new prometheus receiver
	promFactory := prometheusreceiver.NewFactory()

	targets := []*mocks.TestData{
		{
			Name: "kueue_prometheus",
			Pages: []mocks.MockPrometheusResponse{
				{Code: 200, Data: kueueMetrics},
			},
		},
	}
	mp, cfg, err := mocks.SetupMockPrometheus(targets...)
	assert.NoError(t, err)
	defer mp.Close()

	// create a test-specific prometheus config
	scrapeConfig := &config.ScrapeConfig{
		JobName:         kmJobName,
		ScrapeInterval:  cfg.ScrapeConfigs[0].ScrapeInterval,
		ScrapeTimeout:   cfg.ScrapeConfigs[0].ScrapeTimeout,
		ScrapeProtocols: cfg.ScrapeConfigs[0].ScrapeProtocols,
		MetricsPath:     cfg.ScrapeConfigs[0].MetricsPath,
		Scheme:          "http",
		ServiceDiscoveryConfigs: discovery.Configs{
			&discovery.StaticConfig{
				{
					Targets: []model.LabelSet{
						{
							model.AddressLabel: model.LabelValue(strings.Split(mp.Srv.URL, "http://")[1]),
						},
					},
				},
			},
		},
	}
	promConfig := prometheusreceiver.Config{
		PrometheusConfig: &prometheusreceiver.PromConfig{
			ScrapeConfigs: []*config.ScrapeConfig{scrapeConfig},
		},
	}

	// create test receiver
	params := receiver.Settings{
		TelemetrySettings: settings,
	}
	promReceiver, err := promFactory.CreateMetricsReceiver(context.TODO(), params, &promConfig, mConsumer)
	assert.NoError(t, err)

	// attach test receiver to scraper (replaces existing one)
	scraper.prometheusReceiver = promReceiver
	assert.NoError(t, err)
	assert.NotNil(t, mp)
	defer mp.Close()

	// perform a single scrape, this will kick off the scraper process for additional scrapes
	scraper.GetMetrics()

	t.Cleanup(func() {
		scraper.Shutdown()
	})

	// wait for 2 scrapes, one initiated by us, another by the new scraper process
	mp.Wg.Wait()
	mp.Wg.Wait()

	// assert consumer was called and all metrics were processed
	assert.True(t, *mConsumer.called)
	assert.True(t, *mConsumer.pendingWorkloadCount)
	assert.True(t, *mConsumer.activeWorkloadCount)
}

func TestKueuePrometheusScraperJobName(t *testing.T) {
	// needs to start with containerInsights
	assert.True(t, kmJobName == "containerInsightsKueueMetricsScraper")
}
