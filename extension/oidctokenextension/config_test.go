// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap/confmaptest"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name:    "valid",
			cfg:     &Config{Provider: ProviderAzure, OutputTokenFile: "/tmp/token"},
			wantErr: "",
		},
		{
			name:    "missing output_token_file",
			cfg:     &Config{Provider: ProviderAuto},
			wantErr: "output_token_file is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}

func TestConfig_ProviderType(t *testing.T) {
	tests := []struct {
		input   string
		want    ProviderType
		wantErr bool
	}{
		{"auto", ProviderAuto, false},
		{"azure", ProviderAzure, false},
		{"none", ProviderNone, false},
		{"gcp", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var p ProviderType
			err := p.UnmarshalText([]byte(tt.input))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, p)

			// Verify round-trip: marshal back to text
			text, err := p.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, tt.input, string(text))
		})
	}
}

func TestConfig_LoadFromYAML(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)

	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	sub, err := cm.Sub("oidctoken")
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	c := cfg.(*Config)
	assert.Equal(t, ProviderAzure, c.Provider)
	assert.Equal(t, "https://management.azure.com/", c.Audience)
	assert.Equal(t, "/tmp/oidc-token", c.OutputTokenFile)
}

func TestConfig_BuildProviders(t *testing.T) {
	tests := []struct {
		name      string
		provider  ProviderType
		wantNames []string
	}{
		{"none", ProviderNone, []string{}},
		{"azure", ProviderAzure, []string{"azure"}},
		{"auto", ProviderAuto, []string{"azure"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Provider: tt.provider}
			providers := c.buildProviders()
			require.Len(t, providers, len(tt.wantNames))
			names := []string{}
			for _, p := range providers {
				names = append(names, p.Name())
			}
			assert.Equal(t, tt.wantNames, names)
		})
	}
}
