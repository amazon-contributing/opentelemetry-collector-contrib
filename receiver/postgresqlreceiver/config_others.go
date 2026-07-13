//go:build !linux

// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver"

import (
	"fmt"
	"os"
)

func (cfg *Config) validatePassfilePermissions() error {
	if _, err := os.Stat(cfg.Passfile); err != nil {
		return fmt.Errorf("`passfile` is inaccessible: %w", err)
	}
	return nil
}
