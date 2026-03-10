// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor

import (
	"fmt"
)

// Config defines the configuration for the awsattributelimit processor.
type Config struct {
	// MaxTotalAttributes is the maximum combined count of resource attributes,
	// scope attributes, and datapoint attributes allowed per metric datapoint.
	// Defaults to 150, matching the Zeus hard limit.
	MaxTotalAttributes int `mapstructure:"max_total_attributes"`
}

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.MaxTotalAttributes <= 0 {
		return fmt.Errorf("max_total_attributes must be greater than 0, got %d", cfg.MaxTotalAttributes)
	}
	return nil
}
