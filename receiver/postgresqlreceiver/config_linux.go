//go:build linux

// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver"

import (
	"fmt"
	"os"
)

func (cfg *Config) validatePassfilePermissions() error {
	info, err := os.Stat(cfg.Passfile)
	if err != nil {
		return fmt.Errorf("`passfile` is inaccessible: %w", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 && perm != 0o400 {
		return fmt.Errorf("`passfile` permissions must be 0600 or 0400, got %04o: %s", perm, cfg.Passfile)
	}
	return nil
}
