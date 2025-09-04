// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package hostmetricsreceiver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/otelcol/otelcoltest"
	"go.opentelemetry.io/collector/receiver/scraperhelper"
	"gopkg.in/yaml.v3"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/filter/filterset"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/cpuscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/diskscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/filesystemscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/loadscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/memoryscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/networkscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/pagingscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/processesscraper"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/scraper/processscraper"
)

func TestLoadConfig(t *testing.T) {
	factories, err := otelcoltest.NopFactories()
	require.NoError(t, err)

	factory := NewFactory()
	factories.Receivers[metadata.Type] = factory
	// https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/33594
	// nolint:staticcheck
	cfg, err := otelcoltest.LoadConfigAndValidate(filepath.Join("testdata", "config.yaml"), factories)

	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Len(t, cfg.Receivers, 2)

	r0 := cfg.Receivers[component.NewID(metadata.Type)]
	defaultConfigCPUScraper := factory.CreateDefaultConfig()
	defaultConfigCPUScraper.(*Config).Scrapers = map[string]component.Config{
		cpuscraper.TypeStr: func() component.Config {
			cfg := (&cpuscraper.Factory{}).CreateDefaultConfig()
			cfg.SetEnvMap(common.EnvMap{})
			return cfg
		}(),
	}

	assert.Equal(t, defaultConfigCPUScraper, r0)

	r1 := cfg.Receivers[component.NewIDWithName(metadata.Type, "customname")].(*Config)
	expectedConfig := &Config{
		MetadataCollectionInterval: 5 * time.Minute,
		ControllerConfig: scraperhelper.ControllerConfig{
			CollectionInterval: 30 * time.Second,
			InitialDelay:       time.Second,
		},
		Scrapers: map[string]component.Config{
			cpuscraper.TypeStr: func() component.Config {
				cfg := (&cpuscraper.Factory{}).CreateDefaultConfig()
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			}(),
			diskscraper.TypeStr: func() component.Config {
				cfg := (&diskscraper.Factory{}).CreateDefaultConfig()
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			}(),
			loadscraper.TypeStr: (func() component.Config {
				cfg := (&loadscraper.Factory{}).CreateDefaultConfig()
				cfg.(*loadscraper.Config).CPUAverage = true
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			})(),
			filesystemscraper.TypeStr: func() component.Config {
				cfg := (&filesystemscraper.Factory{}).CreateDefaultConfig()
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			}(),
			memoryscraper.TypeStr: func() component.Config {
				cfg := (&memoryscraper.Factory{}).CreateDefaultConfig()
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			}(),
			networkscraper.TypeStr: (func() component.Config {
				cfg := (&networkscraper.Factory{}).CreateDefaultConfig()
				cfg.(*networkscraper.Config).Include = networkscraper.MatchConfig{
					Interfaces: []string{"test1"},
					Config:     filterset.Config{MatchType: "strict"},
				}
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			})(),
			processesscraper.TypeStr: func() component.Config {
				cfg := (&processesscraper.Factory{}).CreateDefaultConfig()
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			}(),
			pagingscraper.TypeStr: func() component.Config {
				cfg := (&pagingscraper.Factory{}).CreateDefaultConfig()
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			}(),
			processscraper.TypeStr: (func() component.Config {
				cfg := (&processscraper.Factory{}).CreateDefaultConfig()
				cfg.(*processscraper.Config).Include = processscraper.MatchConfig{
					Names:  []string{"test2", "test3"},
					Config: filterset.Config{MatchType: "regexp"},
				}
				cfg.SetEnvMap(common.EnvMap{})
				return cfg
			})(),
		},
	}

	assert.Equal(t, expectedConfig, r1)
}

func TestLoadInvalidConfig_NoScrapers(t *testing.T) {
	factories, err := otelcoltest.NopFactories()
	require.NoError(t, err)

	factory := NewFactory()
	factories.Receivers[metadata.Type] = factory
	// https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/33594
	// nolint:staticcheck
	_, err = otelcoltest.LoadConfigAndValidate(filepath.Join("testdata", "config-noscrapers.yaml"), factories)

	require.ErrorContains(t, err, "must specify at least one scraper when using hostmetrics receiver")
}

func TestLoadInvalidConfig_InvalidScraperKey(t *testing.T) {
	factories, err := otelcoltest.NopFactories()
	require.NoError(t, err)

	factory := NewFactory()
	factories.Receivers[metadata.Type] = factory
	// https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/33594
	// nolint:staticcheck
	_, err = otelcoltest.LoadConfigAndValidate(filepath.Join("testdata", "config-invalidscraperkey.yaml"), factories)

	require.ErrorContains(t, err, "error reading configuration for \"hostmetrics\": invalid scraper key: invalidscraperkey")
}
func TestYAMLSerialization(t *testing.T) {
	// Create a config with memory scraper
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig().(*Config)
	
	memFactory := &memoryscraper.Factory{}
	cfg.Scrapers = map[string]component.Config{
		"memory": memFactory.CreateDefaultConfig(),
	}

	// Test YAML marshaling
	yamlData, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	assert.NotEmpty(t, yamlData)

	// Test YAML unmarshaling
	var newCfg Config
	err = yaml.Unmarshal(yamlData, &newCfg)
	require.NoError(t, err)

	// Verify the scrapers field is preserved
	assert.Equal(t, len(cfg.Scrapers), len(newCfg.Scrapers))
}