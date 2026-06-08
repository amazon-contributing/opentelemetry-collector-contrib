// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8staintsprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8staintsprocessor/internal/metadata"
)

func TestNewFactory(t *testing.T) {
	f := NewFactory()
	assert.Equal(t, metadata.Type, f.Type())
	assert.NotNil(t, f.CreateDefaultConfig())
}

func TestCreateMetricsProcessor(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig()
	p, err := f.CreateMetrics(context.Background(), processortest.NewNopSettings(metadata.Type), cfg, consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestCreateLogsProcessor(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig()
	p, err := f.CreateLogs(context.Background(), processortest.NewNopSettings(metadata.Type), cfg, consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, p)
}

func TestSharedProcessorInstance(t *testing.T) {
	f := NewFactory()
	cfg := f.CreateDefaultConfig()
	settings := processortest.NewNopSettings(metadata.Type)

	m, err := f.CreateMetrics(context.Background(), settings, cfg, consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, m)

	l, err := f.CreateLogs(context.Background(), settings, cfg, consumertest.NewNop())
	require.NoError(t, err)
	assert.NotNil(t, l)
}
