// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsattributelimitprocessor"

import (
	"fmt"
)

// Config defines the configuration for the awsattributelimit processor.
type Config struct {
	// MaxTotalAttributes is the maximum combined count of attributes allowed per
	// record: resource + scope + leaf attributes, where the leaf is a metric
	// datapoint's attributes or a profile's.
	// Defaults to 150, matching the aws backend hard limit.
	MaxTotalAttributes int `mapstructure:"max_total_attributes"`

	// UnconditionalRemovalPrefixes is a list of resource attribute key prefixes.
	// Any resource attribute whose key starts with one of these prefixes is always
	// removed, regardless of whether the total count exceeds the limit.
	UnconditionalRemovalPrefixes []string `mapstructure:"unconditional_removal_prefixes"`

	// UnconditionalRemovalKeys is a list of exact resource attribute keys that are
	// always removed, regardless of whether the total count exceeds the limit.
	UnconditionalRemovalKeys []string `mapstructure:"unconditional_removal_keys"`
}

const maxAllowedAttributes = 150

// Validate checks if the processor configuration is valid.
func (cfg *Config) Validate() error {
	if cfg.MaxTotalAttributes <= 0 {
		return fmt.Errorf("max_total_attributes must be greater than 0, got %d", cfg.MaxTotalAttributes)
	}
	if cfg.MaxTotalAttributes > maxAllowedAttributes {
		return fmt.Errorf("max_total_attributes must not exceed %d, got %d", maxAllowedAttributes, cfg.MaxTotalAttributes)
	}
	return nil
}
