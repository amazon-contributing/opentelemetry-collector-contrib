// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "neuron", DeviceIDAttribute: "NeuronDevice", ResourceNames: []string{"aws.amazon.com/neurondevice"}},
		},
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_EmptyBothDeviceTypes(t *testing.T) {
	cfg := &Config{}
	assert.ErrorContains(t, cfg.Validate(), "at least one of device_types or dra_device_types must not be empty")
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{DeviceIDAttribute: "dev", ResourceNames: []string{"res"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "name must not be empty")
}

func TestValidate_MissingDeviceIDAttribute(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", ResourceNames: []string{"res"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "device_id_attribute must not be empty")
}

func TestValidate_MissingResourceNames(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev"},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "resource_names must not be empty")
}

func TestValidate_InvalidDeviceIDSource(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", DeviceIDSource: "invalid", ResourceNames: []string{"res"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "device_id_source must be")
}

func TestValidate_DuplicateName(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev1", ResourceNames: []string{"res1"}},
			{Name: "gpu", DeviceIDAttribute: "dev2", ResourceNames: []string{"res2"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "duplicate name")
}

func TestValidate_DefaultsDeviceIDSource(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", ResourceNames: []string{"res"}},
		},
	}
	require.NoError(t, cfg.Validate())
	assert.Empty(t, cfg.DeviceTypes[0].DeviceIDSource)

	cfg.setDefaults()
	assert.Equal(t, DeviceIDSourceDatapoint, cfg.DeviceTypes[0].DeviceIDSource)
}

// DRA config tests

func TestValidate_DRAOnlyConfig(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DeviceIDAttribute: "gpu_device", DriverNames: []string{"gpu.nvidia.com"}},
		},
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_DRAMissingName(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{DeviceIDAttribute: "dev", DriverNames: []string{"gpu.nvidia.com"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "dra_device_types[0]: name must not be empty")
}

func TestValidate_DRAMissingDeviceIDAttribute(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DriverNames: []string{"gpu.nvidia.com"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "dra_device_types[0]: device_id_attribute must not be empty")
}

func TestValidate_DRAMissingDriverNames(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DeviceIDAttribute: "dev"},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "dra_device_types[0]: driver_names must not be empty")
}

func TestValidate_DRAInvalidDeviceIDSource(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DeviceIDAttribute: "dev", DeviceIDSource: "bad", DriverNames: []string{"gpu.nvidia.com"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "dra_device_types[0]: device_id_source must be")
}

func TestValidate_DRAInvalidRegex(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DeviceIDAttribute: "dev", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: "[invalid"},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "invalid device_id_pattern")
}

func TestValidate_DRAValidRegex(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DeviceIDAttribute: "dev", DriverNames: []string{"gpu.nvidia.com"}, DeviceIDPattern: `gpu-(\d+)`},
		},
	}
	assert.NoError(t, cfg.Validate())
}

func TestValidate_DuplicateNameAcrossTypes(t *testing.T) {
	cfg := &Config{
		DeviceTypes: []DeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", ResourceNames: []string{"nvidia.com/gpu"}},
		},
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu", DeviceIDAttribute: "dev", DriverNames: []string{"gpu.nvidia.com"}},
		},
	}
	assert.ErrorContains(t, cfg.Validate(), "duplicate name")
}

func TestSetDefaults_DRADeviceIDSource(t *testing.T) {
	cfg := &Config{
		DRADeviceTypes: []DRADeviceTypeConfig{
			{Name: "gpu-dra", DeviceIDAttribute: "dev", DriverNames: []string{"gpu.nvidia.com"}},
		},
	}
	cfg.setDefaults()
	assert.Equal(t, DeviceIDSourceDatapoint, cfg.DRADeviceTypes[0].DeviceIDSource)
}
