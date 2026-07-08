// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdeviceslurmjobcorrelationprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "empty device types",
			cfg:     Config{},
			wantErr: "device_types must not be empty",
		},
		{
			name: "missing device name",
			cfg: Config{
				DeviceTypes: []DeviceTypeConfig{{DeviceIDAttribute: "gpu"}},
			},
			wantErr: "device_types[0]: name must not be empty",
		},
		{
			name: "missing device_id_attribute",
			cfg: Config{
				DeviceTypes: []DeviceTypeConfig{{Name: "gpu"}},
			},
			wantErr: "device_types[0]: device_id_attribute must not be empty",
		},
		{
			name: "invalid device_id_source",
			cfg: Config{
				DeviceTypes: []DeviceTypeConfig{{Name: "gpu", DeviceIDAttribute: "gpu", DeviceIDSource: "invalid"}},
			},
			wantErr: "device_types[0]: device_id_source must be",
		},
		{
			name: "duplicate device name",
			cfg: Config{
				DeviceTypes: []DeviceTypeConfig{
					{Name: "gpu", DeviceIDAttribute: "gpu"},
					{Name: "gpu", DeviceIDAttribute: "device"},
				},
			},
			wantErr: "duplicate name",
		},
		{
			name: "invalid cgroup version",
			cfg: Config{
				CgroupVersion: "v3",
				DeviceTypes:   []DeviceTypeConfig{{Name: "gpu", DeviceIDAttribute: "gpu"}},
			},
			wantErr: "cgroup_version must be",
		},
		{
			name: "valid minimal config",
			cfg: Config{
				DeviceTypes: []DeviceTypeConfig{
					{Name: "gpu", DeviceIDAttribute: "gpu"},
				},
			},
		},
		{
			name: "valid full config",
			cfg: Config{
				CgroupRoot:           "/sys/fs/cgroup",
				CgroupVersion:        "v2",
				MetadataPollInterval: 30_000_000_000,
				NodeExclusive:        true,
				DeviceTypes: []DeviceTypeConfig{
					{Name: "gpu", DeviceIDAttribute: "gpu", DeviceIDSource: DeviceIDSourceDatapoint},
					{Name: "efa", DeviceIDAttribute: "aws.efa.device", DeviceIDSource: DeviceIDSourceResource},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConfigSetDefaults(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "gpu"},
		},
	}
	cfg.setDefaults()

	assert.Equal(t, "/sys/fs/cgroup", cfg.CgroupRoot)
	assert.Equal(t, "v1", cfg.CgroupVersion)
	assert.Equal(t, DeviceIDSourceDatapoint, cfg.DeviceTypes[0].DeviceIDSource)
}
