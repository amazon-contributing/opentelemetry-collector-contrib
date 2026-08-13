// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusreceiver

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/metadata"
)

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig()
	assert.NotNil(t, cfg, "failed to create default config")
	assert.NoError(t, componenttest.CheckConfigStruct(cfg))
}

func TestCreateReceiver(t *testing.T) {
	cfg := createDefaultConfig()

	// The default config does not provide scrape_config so we expect that metrics receiver
	// creation must also fail.
	creationSet := receivertest.NewNopSettings(metadata.Type)
	mReceiver, _ := createMetricsReceiver(t.Context(), creationSet, cfg, consumertest.NewNop())
	assert.NotNil(t, mReceiver)
	assert.NotNil(t, mReceiver.(*pReceiver).cfg.PrometheusConfig.GlobalConfig)
}

func TestFactoryCanParseServiceDiscoveryConfigs(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config_sd.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	sub, err := cm.Sub(component.NewIDWithName(metadata.Type, "").String())
	require.NoError(t, err)
	assert.NoError(t, sub.Unmarshal(cfg))
}

func TestMultipleCreateWithAPIServer(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig().(*Config)
	cfg.APIServer = &APIServer{
		Enabled: true,
		ServerConfig: confighttp.ServerConfig{
			Endpoint: "localhost:9090",
		},
	}
	set := receivertest.NewNopSettings(metadata.Type)
	firstRcvr, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	host := componenttest.NewNopHost()
	require.NoError(t, err)
	require.NoError(t, firstRcvr.Start(t.Context(), host))
	require.NoError(t, firstRcvr.Shutdown(t.Context()))
	secondRcvr, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, secondRcvr.Start(t.Context(), host))
	require.NoError(t, secondRcvr.Shutdown(t.Context()))
}

func TestMultipleCreate(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	set := receivertest.NewNopSettings(metadata.Type)
	firstRcvr, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	host := componenttest.NewNopHost()
	require.NoError(t, err)
	require.NoError(t, firstRcvr.Start(t.Context(), host))
	require.NoError(t, firstRcvr.Shutdown(t.Context()))
	secondRcvr, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, secondRcvr.Start(t.Context(), host))
	require.NoError(t, secondRcvr.Shutdown(t.Context()))
}

// Some callers keep several instances alive under one receiver ID and retry Start on a timer, so
// registration must stay per instance rather than process-wide.
func TestConcurrentStartsWithSameID(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	set := receivertest.NewNopSettings(metadata.Type)
	host := componenttest.NewNopHost()

	first, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, first.Start(t.Context(), host))

	second, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, second.Start(t.Context(), host))

	require.NoError(t, second.Shutdown(t.Context()))
	require.NoError(t, first.Shutdown(t.Context()))
}

// A receiver ID can have several live instances at once. Shutting down the first (published)
// instance must not orphan the others from the shared gatherer: the next live instance is
// published instead of the ID going dark.
func TestSharedGathererSurvivesFirstShutdownWithSameID(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	set := receivertest.NewNopSettings(metadata.Type)
	host := componenttest.NewNopHost()

	first, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, first.Start(t.Context(), host))

	second, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)
	require.NoError(t, second.Start(t.Context(), host))

	// The originally-published instance shuts down while the second stays live.
	require.NoError(t, first.Shutdown(t.Context()))

	families, err := SharedGatherer().Gather()
	require.NoError(t, err)
	seen := false
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "receiver" && l.GetValue() == set.ID.String() {
					seen = true
				}
			}
		}
	}
	assert.True(t, seen, "receiver %q must still be published after the first instance shuts down (families=%d)", set.ID.String(), len(families))

	require.NoError(t, second.Shutdown(t.Context()))
	families, err = SharedGatherer().Gather()
	require.NoError(t, err)
	assert.Empty(t, families, "no instances left; shared set must be empty")
}

// One caller has to report every receiver's discovery and scrape metrics, so the gatherer must
// resolve the live set at scrape time and label each series with the receiver it came from.
func TestSharedGathererCoversAllReceivers(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	host := componenttest.NewNopHost()

	var started []component.Component
	for _, name := range []string{"alpha", "beta"} {
		set := receivertest.NewNopSettings(metadata.Type)
		set.ID = component.NewIDWithName(metadata.Type, name)
		rcvr, err := factory.CreateMetrics(t.Context(), set, cfg, consumertest.NewNop())
		require.NoError(t, err)
		require.NoError(t, rcvr.Start(t.Context(), host))
		started = append(started, rcvr)
	}

	families, err := SharedGatherer().Gather()
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
	assert.True(t, seen["prometheus/alpha"], "alpha missing from shared gatherer: %v", seen)
	assert.True(t, seen["prometheus/beta"], "beta missing from shared gatherer: %v", seen)

	for _, rcvr := range started {
		require.NoError(t, rcvr.Shutdown(t.Context()))
	}
	families, err = SharedGatherer().Gather()
	require.NoError(t, err)
	assert.Empty(t, families, "shutdown receivers should leave the shared set")
}
