// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	sub, err := cm.Sub("attributestocontext")
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	assert.NoError(t, xconfmap.Validate(cfg))
	assert.Equal(t, &Config{
		Actions: []actions.KeyValue{
			{Key: "cwlogs.log_group", FromResourceAttribute: "cwlogs.log_group"},
			{Key: "cwlogs.log_stream", FromResourceAttribute: "cwlogs.log_stream"},
		},
	}, cfg)
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		config  *Config
		wantErr string
	}{
		{
			name: "valid",
			config: &Config{
				Actions: []actions.KeyValue{
					{Key: "key1", FromResourceAttribute: "service.name"},
				},
			},
		},
		{
			name: "empty actions",
			config: &Config{
				Actions: []actions.KeyValue{},
			},
			wantErr: "missing required field \"actions\"",
		},
		{
			name:    "nil actions",
			config:  &Config{},
			wantErr: "missing required field \"actions\"",
		},
		{
			name: "missing key",
			config: &Config{
				Actions: []actions.KeyValue{
					{FromResourceAttribute: "service.name"},
				},
			},
			wantErr: "action 0: missing required field \"key\"",
		},
		{
			name: "missing from_resource_attribute",
			config: &Config{
				Actions: []actions.KeyValue{
					{Key: "key1"},
				},
			},
			wantErr: "action 0: missing required field \"from_resource_attribute\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}
