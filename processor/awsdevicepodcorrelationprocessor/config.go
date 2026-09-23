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

	// DRADeviceIDAttribute selects the *source* from which the DRA correlation key
	// is derived. When empty (the default), the source is the DRA device name from
	// the ResourceClaim allocation result. When set, it names a ResourceSlice
	// device attribute whose value is used as the source instead — for drivers
	// whose device name does not match the metric label but an attribute does.
	// Example (EFA via dra.net): device name "pci-0000-00-1f-0" does not match the
	// metric label "rdmap0s31", but the attribute "dra.net/rdmaDevice" holds
	// "rdmap0s31"; setting this to "dra.net/rdmaDevice" bridges them. Reading from
	// an attribute requires a ResourceSlice informer (and resourceslices RBAC).
	//
	// This is the DRA/cluster-side key source and is distinct from
	// device_id_attribute above, which names the metric label to match against.
	DRADeviceIDAttribute string `mapstructure:"dra_device_id_attribute"`

	// DRADeviceIDPattern is an optional regex applied to the resolved source
	// string (the device name, or the attribute value selected by
	// dra_device_id_attribute) to extract the value that should match the metric
	// attribute. The first capture group is used. If empty, the source string is
	// used as-is. Example: "gpu-(\d+)" extracts "0" from device name "gpu-0".
	// It composes with either source, so it may also normalize an attribute value.
	DRADeviceIDPattern string `mapstructure:"dra_device_id_pattern"`
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

	// A DRA driver may appear in only one dra_device_types entry: the store keys
	// its device->pod map by driver name, so a driver listed twice (with
	// different dra_device_id_attribute / dra_device_id_pattern) would have one
	// entry's keying silently overwrite the other's. Reject that at config time.
	seenDrivers := make(map[string]bool)
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
		for _, dn := range dt.DriverNames {
			if dn == "" {
				return fmt.Errorf("dra_device_types[%d]: driver_names must not contain an empty string", i)
			}
			if seenDrivers[dn] {
				return fmt.Errorf("dra_device_types[%d]: duplicate driver name %q across dra_device_types entries; "+
					"each DRA driver may appear in only one entry because correlation is keyed by driver", i, dn)
			}
			seenDrivers[dn] = true
		}
		if dt.DeviceIDSource != "" && dt.DeviceIDSource != DeviceIDSourceDatapoint && dt.DeviceIDSource != DeviceIDSourceResource {
			return fmt.Errorf("dra_device_types[%d]: device_id_source must be %q or %q, got %q", i, DeviceIDSourceDatapoint, DeviceIDSourceResource, dt.DeviceIDSource)
		}
		if dt.DRADeviceIDPattern != "" {
			if _, err := regexp.Compile(dt.DRADeviceIDPattern); err != nil {
				return fmt.Errorf("dra_device_types[%d]: invalid dra_device_id_pattern: %w", i, err)
			}
		}
		if seen[dt.Name] {
			return fmt.Errorf("dra_device_types[%d]: duplicate name %q", i, dt.Name)
		}
		seen[dt.Name] = true
	}

	return nil
}
