// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusscraper

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"
)

func minimalScrapeConfig(job string) *config.ScrapeConfig {
	return &config.ScrapeConfig{
		JobName:         job,
		ScrapeProtocols: config.DefaultScrapeProtocols,
		ScrapeInterval:  model.Duration(30 * time.Second),
		ScrapeTimeout:   model.Duration(10 * time.Second),
		Scheme:          "http",
		MetricsPath:     "/metrics",
		ServiceDiscoveryConfigs: discovery.Configs{
			&discovery.StaticConfig{
				{Targets: []model.LabelSet{{model.AddressLabel: model.LabelValue("127.0.0.1:19999")}}},
			},
		},
	}
}

// Each scraper in a process must register under its own receiver ID so the process-wide shared
// gatherer reports every one of them. Regression guard for the same-ID ("prometheus") collision.
func TestScrapersRegisterUnderDistinctIDs(t *testing.T) {
	settings := componenttest.NewNopTelemetrySettings()
	host := componenttest.NewNopHost()

	names := []string{"dcgm", "neuron"}
	for _, name := range names {
		scraper, err := NewSimplePrometheusScraper(SimplePrometheusScraperOpts{
			Ctx:               t.Context(),
			Name:              name,
			TelemetrySettings: settings,
			Consumer:          consumertest.NewNop(),
			Host:              host,
			HostInfoProvider:  mockHostInfoProvider{},
			ScraperConfigs:    minimalScrapeConfig(name),
			Logger:            zap.NewNop(),
		})
		require.NoError(t, err)
		scraper.GetMetrics() // starts the embedded receiver -> registers into the shared gatherer
		t.Cleanup(scraper.Shutdown)
	}

	families, err := prometheusreceiver.SharedGatherer().Gather()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "receiver" {
					seen[l.GetValue()] = true
				}
			}
		}
	}
	assert.True(t, seen["prometheus/dcgm"], "dcgm scraper missing from shared gatherer: %v", seen)
	assert.True(t, seen["prometheus/neuron"], "neuron scraper missing from shared gatherer: %v", seen)
}
