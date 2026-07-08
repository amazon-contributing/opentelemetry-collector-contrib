// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdeviceslurmjobcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor"

import (
	"errors"
	"fmt"
	"time"
)

// Config defines the configuration for the awsdevicejobcorrelation processor.
type Config struct {
	// CgroupRoot is the root path for cgroup filesystem.
	// Defaults to "/sys/fs/cgroup".
	CgroupRoot string `mapstructure:"cgroup_root"`

	// CgroupVersion is the cgroup version in use: "v1" or "v2".
	// Defaults to "v1".
	CgroupVersion string `mapstructure:"cgroup_version"`

	// MetadataPollInterval is how frequently to refresh job metadata from scontrol.
	// Defaults to 30s.
	MetadataPollInterval time.Duration `mapstructure:"metadata_poll_interval"`

	// NodeExclusive indicates whether compute nodes run a single job at a time.
	// When true, all devices on a node are attributed to the one running job.
	// This simplifies EFA attribution where per-process counters aren't available.
	// Defaults to true.
	NodeExclusive bool `mapstructure:"node_exclusive"`

	// DeviceTypes defines device types and their ID attributes for correlation.
	DeviceTypes []DeviceTypeConfig `mapstructure:"device_types"`

	// HostPath is a prefix for accessing the host filesystem from a container.
	// Set to "/host" if running in a container with host filesystem mounted.
	// Defaults to "" (running directly on host).
	HostPath string `mapstructure:"host_path"`
}

// DeviceIDSource indicates where the device ID attribute lives in the OTEL data model.
type DeviceIDSource string

const (
	DeviceIDSourceDatapoint DeviceIDSource = "datapoint"
	DeviceIDSourceResource  DeviceIDSource = "resource"
)

// DeviceTypeConfig defines correlation settings for a single device type.
type DeviceTypeConfig struct {
	// Name uniquely identifies this device type (e.g., "gpu", "efa").
	Name string `mapstructure:"name"`

	// DeviceIDAttribute is the metric attribute key holding the device identifier.
	// For DCGM: "gpu" (value "0", "1", etc.)
	// For EFA: "aws.efa.device" (value "efa_0", etc.)
	DeviceIDAttribute string `mapstructure:"device_id_attribute"`

	// DeviceIDSource indicates whether device_id_attribute is on the datapoint or resource.
	// Defaults to "datapoint".
	DeviceIDSource DeviceIDSource `mapstructure:"device_id_source"`
}

func (cfg *Config) setDefaults() {
	if cfg.CgroupRoot == "" {
		cfg.CgroupRoot = "/sys/fs/cgroup"
	}
	if cfg.CgroupVersion == "" {
		cfg.CgroupVersion = "v1"
	}
	if cfg.MetadataPollInterval == 0 {
		cfg.MetadataPollInterval = 30 * time.Second
	}
	for i := range cfg.DeviceTypes {
		if cfg.DeviceTypes[i].DeviceIDSource == "" {
			cfg.DeviceTypes[i].DeviceIDSource = DeviceIDSourceDatapoint
		}
	}
}

func (cfg *Config) Validate() error {
	if len(cfg.DeviceTypes) == 0 {
		return errors.New("device_types must not be empty")
	}

	if cfg.CgroupVersion != "" && cfg.CgroupVersion != "v1" && cfg.CgroupVersion != "v2" {
		return fmt.Errorf("cgroup_version must be \"v1\" or \"v2\", got %q", cfg.CgroupVersion)
	}

	seen := make(map[string]bool)
	for i, dt := range cfg.DeviceTypes {
		if dt.Name == "" {
			return fmt.Errorf("device_types[%d]: name must not be empty", i)
		}
		if dt.DeviceIDAttribute == "" {
			return fmt.Errorf("device_types[%d]: device_id_attribute must not be empty", i)
		}
		if dt.DeviceIDSource != "" && dt.DeviceIDSource != DeviceIDSourceDatapoint && dt.DeviceIDSource != DeviceIDSourceResource {
			return fmt.Errorf("device_types[%d]: device_id_source must be %q or %q", i, DeviceIDSourceDatapoint, DeviceIDSourceResource)
		}
		if seen[dt.Name] {
			return fmt.Errorf("device_types[%d]: duplicate name %q", i, dt.Name)
		}
		seen[dt.Name] = true
	}

	return nil
}
