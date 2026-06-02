// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor"

import (
	"errors"
	"fmt"
	"regexp"
)

// Config defines the configuration for the awsdevicepodcorrelation processor.
type Config struct {
	// KubeletSocketPath is the path to the Kubelet Pod Resources API socket.
	// Defaults to "/var/lib/kubelet/pod-resources/kubelet.sock".
	KubeletSocketPath string `mapstructure:"kubelet_socket_path"`

	// DeviceTypes is a list of device type configurations, each defining how to
	// correlate one class of devices (e.g., neuron, EFA, GPU) with pod metadata
	// using the Kubelet device plugin interface.
	DeviceTypes []DeviceTypeConfig `mapstructure:"device_types"`

	// DRADeviceTypes is a list of device type configurations for devices allocated
	// via Kubernetes Dynamic Resource Allocation (DRA). Uses K8s API informers
	// watching ResourceClaims and Pods. Optional — if empty, the DRA code path
	// is not activated.
	DRADeviceTypes []DRADeviceTypeConfig `mapstructure:"dra_device_types"`

	// NodeName is the Kubernetes node name used to filter pods in the DRA path.
	// If empty, the NODE_NAME environment variable is used. Only relevant when
	// dra_device_types is configured.
	NodeName string `mapstructure:"node_name"`
}

// DeviceIDSource indicates where the device ID attribute lives in the OTEL data model.
type DeviceIDSource string

const (
	// DeviceIDSourceDatapoint means the device ID is a datapoint-level attribute.
	DeviceIDSourceDatapoint DeviceIDSource = "datapoint"
	// DeviceIDSourceResource means the device ID is a resource-level attribute.
	DeviceIDSourceResource DeviceIDSource = "resource"
)

// DeviceTypeConfig defines the configuration for a single device type.
type DeviceTypeConfig struct {
	// Name uniquely identifies this device type (e.g., "neuron", "efa", "gpu").
	Name string `mapstructure:"name"`

	// DeviceIDAttribute is the metric attribute key that holds the device identifier
	// used for PodResourcesStore lookup (e.g., "NeuronDevice", "device").
	DeviceIDAttribute string `mapstructure:"device_id_attribute"`

	// DeviceIDSource indicates whether device_id_attribute is found on the
	// datapoint ("datapoint") or on the resource ("resource"). Defaults to "datapoint".
	DeviceIDSource DeviceIDSource `mapstructure:"device_id_source"`

	// ResourceNames is an ordered list of Kubernetes extended resource names to try
	// during PodResourcesStore lookup (e.g., ["aws.amazon.com/neurondevice", "aws.amazon.com/neuron"]).
	// The processor tries each in order until a match is found.
	ResourceNames []string `mapstructure:"resource_names"`
}

// DRADeviceTypeConfig defines the configuration for a single DRA-allocated device type.
type DRADeviceTypeConfig struct {
	// Name uniquely identifies this DRA device type (e.g., "neuron-dra", "gpu-dra").
	Name string `mapstructure:"name"`

	// DeviceIDAttribute is the metric attribute key that holds the device identifier.
	DeviceIDAttribute string `mapstructure:"device_id_attribute"`

	// DeviceIDSource indicates whether device_id_attribute is found on the
	// datapoint ("datapoint") or on the resource ("resource"). Defaults to "datapoint".
	DeviceIDSource DeviceIDSource `mapstructure:"device_id_source"`

	// DriverNames is the list of DRA driver names to match against in the
	// ResourceClaim allocation results (e.g., ["gpu.nvidia.com", "neuron.aws.com"]).
	DriverNames []string `mapstructure:"driver_names"`

	// DeviceIDPattern is a regex applied to the DRA device name to extract
	// the value that should match the metric attribute. The first capture group
	// is used. If empty, the full device name is used as-is.
	// Example: "gpu-(\d+)" extracts "0" from "gpu-0".
	DeviceIDPattern string `mapstructure:"device_id_pattern"`
}

// setDefaults applies default values to the configuration.
func (cfg *Config) setDefaults() {
	for i := range cfg.DeviceTypes {
		if cfg.DeviceTypes[i].DeviceIDSource == "" {
			cfg.DeviceTypes[i].DeviceIDSource = DeviceIDSourceDatapoint
		}
	}
	for i := range cfg.DRADeviceTypes {
		if cfg.DRADeviceTypes[i].DeviceIDSource == "" {
			cfg.DRADeviceTypes[i].DeviceIDSource = DeviceIDSourceDatapoint
		}
	}
}

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	if len(cfg.DeviceTypes) == 0 && len(cfg.DRADeviceTypes) == 0 {
		return errors.New("at least one of device_types or dra_device_types must not be empty")
	}

	seen := make(map[string]bool, len(cfg.DeviceTypes)+len(cfg.DRADeviceTypes))
	for i := range cfg.DeviceTypes {
		dt := &cfg.DeviceTypes[i]
		if dt.Name == "" {
			return fmt.Errorf("device_types[%d]: name must not be empty", i)
		}
		if dt.DeviceIDAttribute == "" {
			return fmt.Errorf("device_types[%d]: device_id_attribute must not be empty", i)
		}
		if len(dt.ResourceNames) == 0 {
			return fmt.Errorf("device_types[%d]: resource_names must not be empty", i)
		}
		if dt.DeviceIDSource != "" && dt.DeviceIDSource != DeviceIDSourceDatapoint && dt.DeviceIDSource != DeviceIDSourceResource {
			return fmt.Errorf("device_types[%d]: device_id_source must be %q or %q, got %q", i, DeviceIDSourceDatapoint, DeviceIDSourceResource, dt.DeviceIDSource)
		}
		if seen[dt.Name] {
			return fmt.Errorf("device_types[%d]: duplicate name %q", i, dt.Name)
		}
		seen[dt.Name] = true
	}

	for i := range cfg.DRADeviceTypes {
		dt := &cfg.DRADeviceTypes[i]
		if dt.Name == "" {
			return fmt.Errorf("dra_device_types[%d]: name must not be empty", i)
		}
		if dt.DeviceIDAttribute == "" {
			return fmt.Errorf("dra_device_types[%d]: device_id_attribute must not be empty", i)
		}
		if len(dt.DriverNames) == 0 {
			return fmt.Errorf("dra_device_types[%d]: driver_names must not be empty", i)
		}
		if dt.DeviceIDSource != "" && dt.DeviceIDSource != DeviceIDSourceDatapoint && dt.DeviceIDSource != DeviceIDSourceResource {
			return fmt.Errorf("dra_device_types[%d]: device_id_source must be %q or %q, got %q", i, DeviceIDSourceDatapoint, DeviceIDSourceResource, dt.DeviceIDSource)
		}
		if dt.DeviceIDPattern != "" {
			if _, err := regexp.Compile(dt.DeviceIDPattern); err != nil {
				return fmt.Errorf("dra_device_types[%d]: invalid device_id_pattern: %w", i, err)
			}
		}
		if seen[dt.Name] {
			return fmt.Errorf("dra_device_types[%d]: duplicate name %q", i, dt.Name)
		}
		seen[dt.Name] = true
	}

	return nil
}
