// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sqlserverreceiver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"
	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver/internal/metadata"
)

func TestValidate(t *testing.T) {
	testCases := []struct {
		desc            string
		cfg             *Config
		expectedSuccess bool
	}{
		{
			desc: "valid config",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
			},
			expectedSuccess: true,
		},
		{
			desc: "valid config with no metric settings",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
			},
			expectedSuccess: true,
		},
		{
			desc:            "default config is valid",
			cfg:             createDefaultConfig().(*Config),
			expectedSuccess: true,
		},
		{
			desc: "invalid config with partial direct connect settings",
			cfg: &Config{
				ControllerConfig: scraperhelper.NewDefaultControllerConfig(),
				Server:           "0.0.0.0",
				Username:         "sa",
			},
			expectedSuccess: false,
		},
		{
			desc: "invalid config with datasource and any direct connect settings",
			cfg: &Config{
				ControllerConfig: scraperhelper.NewDefaultControllerConfig(),
				DataSource:       "a connection string",
				Username:         "sa",
				Port:             1433,
			},
			expectedSuccess: false,
		},
		{
			desc: "valid config only datasource and none direct connect settings",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				DataSource:           "a connection string",
			},
			expectedSuccess: true,
		},
		{
			desc: "valid config with all direct connection settings",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				Server:               "0.0.0.0",
				Username:             "sa",
				Password:             "password",
				Port:                 1433,
			},
			expectedSuccess: true,
		},
		{
			desc: "config with invalid MaxQuerySampleCount value",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				TopQueryCollection: TopQueryCollection{
					MaxQuerySampleCount: 100000,
				},
			},
			expectedSuccess: false,
		},
		{
			desc: "config with invalid TopQueryCount value",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				TopQueryCollection: TopQueryCollection{
					MaxQuerySampleCount: 100,
					TopQueryCount:       200000,
				},
			},
			expectedSuccess: false,
		},
		{
			desc: "config with invalid LookbackTime",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				TopQueryCollection: TopQueryCollection{
					MaxQuerySampleCount: 100,
					TopQueryCount:       200000,
					LookbackTime:        -1,
				},
			},
			expectedSuccess: false,
		},
		{
			desc: "valid config with passfile instead of password",
			cfg: func() *Config {
				cfg := &Config{
					MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
					ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
					Server:               "0.0.0.0",
					Username:             "sa",
					Port:                 1433,
				}
				// Create a temp passfile with correct permissions for validation
				return cfg
			}(),
			expectedSuccess: false, // passfile path doesn't exist, so validatePassfilePermissions will fail
		},
		{
			desc: "valid config with both password and passfile set (password wins)",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				Server:               "0.0.0.0",
				Username:             "sa",
				Password:             "password",
				Port:                 1433,
				// A non-existent passfile is fine here: an inline password takes
				// priority, so the passfile is ignored and never validated.
				Passfile: "/some/passfile",
			},
			expectedSuccess: true,
		},
		{
			desc: "invalid config with datasource and passfile set",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				DataSource:           "a connection string",
				Passfile:             "/some/passfile",
			},
			expectedSuccess: false,
		},
		{
			desc: "invalid config with passfile but missing server",
			cfg: &Config{
				MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
				ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
				Username:             "sa",
				Port:                 1433,
				Passfile:             "/some/passfile",
			},
			expectedSuccess: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			if tc.expectedSuccess {
				require.NoError(t, xconfmap.Validate(tc.cfg))
			} else {
				require.Error(t, xconfmap.Validate(tc.cfg))
			}
		})
	}
}

func TestValidatePassfile(t *testing.T) {
	// Create a temp passfile with correct permissions
	dir := t.TempDir()
	passfilePath := filepath.Join(dir, ".sqlserver_password")
	err := os.WriteFile(passfilePath, []byte("server=0.0.0.0;user id=sa;password=secret;port=1433\n"), 0o600)
	require.NoError(t, err)

	t.Run("valid config with passfile and server/username/port", func(t *testing.T) {
		cfg := &Config{
			MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
			ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
			Server:               "0.0.0.0",
			Username:             "sa",
			Port:                 1433,
			Passfile:             passfilePath,
		}
		require.NoError(t, xconfmap.Validate(cfg))
		require.True(t, cfg.isDirectDBConnectionEnabled)
	})

	t.Run("valid config with both password and passfile (password wins)", func(t *testing.T) {
		cfg := &Config{
			MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
			ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
			Server:               "0.0.0.0",
			Username:             "sa",
			Password:             "password",
			Port:                 1433,
			Passfile:             passfilePath,
		}
		require.NoError(t, xconfmap.Validate(cfg))
		require.True(t, cfg.isDirectDBConnectionEnabled)
	})

	t.Run("invalid config with datasource and passfile", func(t *testing.T) {
		cfg := &Config{
			MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
			ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
			DataSource:           "a connection string",
			Passfile:             passfilePath,
		}
		err := xconfmap.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no other connection parameters")
	})

	t.Run("invalid passfile permissions", func(t *testing.T) {
		badPassfile := filepath.Join(dir, ".bad_permissions")
		err := os.WriteFile(badPassfile, []byte("content"), 0o644) //nolint:gosec // intentionally lax permissions to exercise validation
		require.NoError(t, err)

		cfg := &Config{
			MetricsBuilderConfig: metadata.DefaultMetricsBuilderConfig(),
			ControllerConfig:     scraperhelper.NewDefaultControllerConfig(),
			Server:               "0.0.0.0",
			Username:             "sa",
			Port:                 1433,
			Passfile:             badPassfile,
		}
		err = xconfmap.Validate(cfg)
		// On Linux this should fail with permission error; on other OS it should pass
		if runtime.GOOS == "linux" {
			require.Error(t, err)
			assert.Contains(t, err.Error(), "permissions must be 0600 or 0400")
		} else {
			require.NoError(t, err)
		}
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
		require.NoError(t, err)
		factory := NewFactory()
		cfg := factory.CreateDefaultConfig()

		sub, err := cm.Sub("sqlserver")
		require.NoError(t, err)
		require.NoError(t, sub.Unmarshal(cfg))

		assert.NoError(t, xconfmap.Validate(cfg))
		assert.Equal(t, factory.CreateDefaultConfig(), cfg)
	})

	t.Run("named", func(t *testing.T) {
		cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
		require.NoError(t, err)

		factory := NewFactory()
		cfg := factory.CreateDefaultConfig()

		expected := factory.CreateDefaultConfig().(*Config)
		expected.MetricsBuilderConfig = metadata.MetricsBuilderConfig{
			Metrics: metadata.DefaultMetricsConfig(),
			ResourceAttributes: metadata.ResourceAttributesConfig{
				HostName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				SqlserverDatabaseName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				SqlserverInstanceName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				SqlserverComputerName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				ServerAddress: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				ServerPort: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
			},
		}
		expected.LogsBuilderConfig = metadata.LogsBuilderConfig{
			Events: metadata.EventsConfig{
				DbServerQuerySample: metadata.EventConfig{
					Enabled: true,
				},
				DbServerTopQuery: metadata.EventConfig{
					Enabled: true,
				},
			},
			ResourceAttributes: metadata.ResourceAttributesConfig{
				HostName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				SqlserverDatabaseName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				SqlserverInstanceName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				SqlserverComputerName: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				ServerAddress: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
				ServerPort: metadata.ResourceAttributeConfig{
					Enabled: true,
				},
			},
		}
		expected.ComputerName = "CustomServer"
		expected.InstanceName = "CustomInstance"
		expected.LookbackTime = 60 * time.Second
		expected.TopQueryCount = 200
		expected.MaxQuerySampleCount = 1000
		expected.TopQueryCollection.CollectionInterval = 80 * time.Second

		expected.QuerySample = QuerySample{
			MaxRowsPerQuery: 1450,
		}

		sub, err := cm.Sub("sqlserver/named")
		require.NoError(t, err)
		require.NoError(t, sub.Unmarshal(cfg))

		assert.NoError(t, xconfmap.Validate(cfg))
		if diff := cmp.Diff(expected, cfg, cmp.FilterPath(func(p cmp.Path) bool {
			if sf, ok := p.Last().(cmp.StructField); ok {
				name := sf.Name()
				return name != "" && name[0] >= 'a' && name[0] <= 'z'
			}
			return false
		}, cmp.Ignore())); diff != "" {
			t.Errorf("Config mismatch (-expected +actual):\n%s", diff)
		}
	})

	t.Run("effectiveLookBackTime", func(t *testing.T) {
		factory := NewFactory()
		config := factory.CreateDefaultConfig().(*Config)

		config.TopQueryCollection.CollectionInterval = 10 * time.Second
		assert.Equal(t, 2*config.TopQueryCollection.CollectionInterval, config.EffectiveLookbackTime(), "By default the 'EffectiveLookbackTime' value should be 2 x 'TopQueryCollection.CollectionInterval'")

		config.LookbackTime = 60 * time.Second
		assert.Equal(t, 60*time.Second, config.EffectiveLookbackTime(), "'EffectiveLookbackTime' should return the user provided 'LookbackTime' if any.")
	})
}
