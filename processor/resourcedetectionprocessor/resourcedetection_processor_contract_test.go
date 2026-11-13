// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package resourcedetectionprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/resourcedetectionprocessor/internal/env"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/resourcedetectionprocessor/internal/metadata"
)

// TestContract_IgnoreDetectorErrorsThroughFactory pins ignore_detector_errors end to end
// through NewFactory: config, factory wiring, the Start() gate, and data flow afterwards.
func TestContract_IgnoreDetectorErrorsThroughFactory(t *testing.T) {
	tests := []struct {
		name                 string
		ignoreDetectorErrors bool
	}{
		{name: "detector error fails start by default", ignoreDetectorErrors: false},
		{name: "detector error is ignored when configured", ignoreDetectorErrors: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The env detector rejects a value without key=value pairs.
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "invalid")

			factory := NewFactory()
			cfg := factory.CreateDefaultConfig().(*Config)
			require.Equal(t, []string{env.TypeStr}, cfg.Detectors)
			cfg.Retry.Enabled = false
			cfg.IgnoreDetectorErrors = tt.ignoreDetectorErrors

			sink := new(consumertest.MetricsSink)
			mp, err := factory.CreateMetrics(t.Context(), processortest.NewNopSettings(metadata.Type), cfg, sink)
			require.NoError(t, err)

			err = mp.Start(t.Context(), componenttest.NewNopHost())
			if !tt.ignoreDetectorErrors {
				require.ErrorContains(t, err, "invalid resource format")
				require.NoError(t, mp.Shutdown(t.Context()))
				return
			}
			require.NoError(t, err)

			md := pmetric.NewMetrics()
			rm := md.ResourceMetrics().AppendEmpty()
			rm.Resource().Attributes().PutStr("service.name", "svc")
			rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetName("m")
			require.NoError(t, mp.ConsumeMetrics(t.Context(), md))

			require.Len(t, sink.AllMetrics(), 1)
			got := sink.AllMetrics()[0]
			assert.Equal(t, 1, got.MetricCount())
			assert.Equal(t, map[string]any{"service.name": "svc"}, got.ResourceMetrics().At(0).Resource().Attributes().AsRaw())

			require.NoError(t, mp.Shutdown(t.Context()))
		})
	}
}
