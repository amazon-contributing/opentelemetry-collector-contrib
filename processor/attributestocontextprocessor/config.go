// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor"

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"
)

// Config defines the configuration for the attributestocontext processor.
type Config struct {
	Actions []actions.KeyValue `mapstructure:"actions"`
}

var _ component.Config = (*Config)(nil)

func (cfg *Config) Validate() error {
	if len(cfg.Actions) == 0 {
		return errors.New("missing required field \"actions\"")
	}

	for i, action := range cfg.Actions {
		if action.Key == "" {
			return fmt.Errorf("action %d: missing required field \"key\"", i)
		}
		if action.FromResourceAttribute == "" {
			return fmt.Errorf("action %d: missing required field \"from_resource_attribute\"", i)
		}
	}

	return nil
}
