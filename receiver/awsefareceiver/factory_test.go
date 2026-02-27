// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsefareceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsefareceiver/internal/metadata"
)

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig()
	require.NotNil(t, cfg)

	efaCfg, ok := cfg.(*Config)
	require.True(t, ok)
	assert.Equal(t, scraperhelper.NewDefaultControllerConfig(), efaCfg.ControllerConfig)
	assert.Equal(t, metadata.DefaultMetricsBuilderConfig(), efaCfg.MetricsBuilderConfig)
	assert.Empty(t, efaCfg.HostPath)
}

func TestCreateMetricsReceiver(t *testing.T) {
	cfg := createDefaultConfig()
	settings := receivertest.NewNopSettings(metadata.Type)
	consumer := consumertest.NewNop()

	recv, err := createMetricsReceiver(t.Context(), settings, cfg, consumer)
	require.NoError(t, err)
	require.NotNil(t, recv)
}

func TestNewFactory(t *testing.T) {
	f := NewFactory()
	require.NotNil(t, f)
	assert.Equal(t, metadata.Type, f.Type())
}
