// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package sqlserverreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver"

import (
	"fmt"
	"os"
	"runtime"
)

func (*Config) validateInstanceAndComputerName() error {
	return nil
}

// validatePassfilePermissions checks that the passfile is accessible on
// non-Windows platforms. On Linux, it additionally enforces strict permissions.
func (cfg *Config) validatePassfilePermissions() error {
	info, err := os.Stat(cfg.Passfile)
	if err != nil {
		return fmt.Errorf("`passfile` is inaccessible: %w", err)
	}

	// On Linux, enforce strict permissions (0600 or 0400 only)
	if runtime.GOOS == "linux" {
		perm := info.Mode().Perm()
		if perm != 0o600 && perm != 0o400 {
			return fmt.Errorf("`passfile` permissions must be 0600 or 0400, got %#o", perm)
		}
	}

	return nil
}
