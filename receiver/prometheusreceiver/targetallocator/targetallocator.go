// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package targetallocator provides a public wrapper for the internal targetallocator package.
package targetallocator // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/targetallocator"

import (
	promconfig "github.com/prometheus/prometheus/config"
	"go.opentelemetry.io/collector/receiver"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/targetallocator"
)

type Config = targetallocator.Config

type Manager = targetallocator.Manager

func NewManager(set receiver.Settings, cfg *Config, promCfg *promconfig.Config) *Manager {
	return targetallocator.NewManager(set, cfg, promCfg)
}
