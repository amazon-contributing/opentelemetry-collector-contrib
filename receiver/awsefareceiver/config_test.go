// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsefareceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name     string
		hostPath string
		wantErr  string
		wantPath string
	}{
		{"empty", "", "", ""},
		{"absolute", "/host", "", "/host"},
		{"relative", "relative/path", "must be an absolute path", ""},
		{"trailing_slash", "/host/", "", "/host/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := createDefaultConfig().(*Config)
			cfg.HostPath = tt.hostPath
			err := cfg.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				if tt.wantPath != "" {
					assert.Equal(t, tt.wantPath, cfg.HostPath)
				}
			}
		})
	}
}
