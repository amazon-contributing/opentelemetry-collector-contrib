// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package targetallocator

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"
	promconfig "github.com/prometheus/prometheus/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/receiver"
)

func TestConfigTypeAlias(t *testing.T) {
	cfg := &Config{
		ClientConfig: confighttp.ClientConfig{
			Endpoint: "http://test-allocator:8080",
		},
		Interval:    30 * time.Second,
		CollectorID: "test-collector",
	}

	assert.NotNil(t, cfg)
	assert.Equal(t, "http://test-allocator:8080", cfg.Endpoint)
	assert.Equal(t, 30*time.Second, cfg.Interval)
	assert.Equal(t, "test-collector", cfg.CollectorID)
}

func TestManagerTypeAlias(t *testing.T) {
	cfg := &Config{
		ClientConfig: confighttp.ClientConfig{
			Endpoint: "http://test-allocator:8080",
		},
		Interval:    30 * time.Second,
		CollectorID: "test-collector",
	}

	promCfg := &promconfig.Config{}

	set := receiver.Settings{
		ID:                component.MustNewID("prometheus"),
		TelemetrySettings: componenttest.NewNopTelemetrySettings(),
	}

	manager := NewManager(set, cfg, promCfg)
	require.NotNil(t, manager)

	// Verify the manager is of the correct type by checking it has the expected methods
	// We can't call Start without proper setup, but we can verify the type exists
	assert.IsType(t, &Manager{}, manager)
}

func TestNewManagerFunction(t *testing.T) {
	cfg := &Config{
		ClientConfig: confighttp.ClientConfig{
			Endpoint: "http://localhost:8080",
		},
		Interval:    60 * time.Second,
		CollectorID: "collector-1",
	}

	promCfg := &promconfig.Config{
		GlobalConfig: promconfig.GlobalConfig{
			ScrapeInterval: model.Duration(15 * time.Second),
			ScrapeTimeout:  model.Duration(10 * time.Second),
		},
	}

	set := receiver.Settings{
		ID:                component.MustNewID("prometheus"),
		TelemetrySettings: componenttest.NewNopTelemetrySettings(),
	}

	manager := NewManager(set, cfg, promCfg)

	assert.NotNil(t, manager, "NewManager should return a non-nil Manager")
	assert.IsType(t, &Manager{}, manager, "NewManager should return a *Manager type")
}
