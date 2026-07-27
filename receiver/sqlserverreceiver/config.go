// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sqlserverreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver"

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/scraper/scraperhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver/internal/metadata"
)

type QuerySample struct {
	MaxRowsPerQuery uint64 `mapstructure:"max_rows_per_query"`

	// prevent unkeyed literal initialization
	_ struct{}
}

type TopQueryCollection struct {
	// Enabled enables the collection of the top queries by the execution time.
	// It will collect the top N queries based on totalElapsedTimeDiffs during the last collection interval.
	// The query statement will also be reported, hence, it is not ideal to send it as a metric. Hence
	// we are reporting them as logs.
	// The `N` is configured via `TopQueryCount`
	LookbackTime        time.Duration `mapstructure:"lookback_time"`
	MaxQuerySampleCount uint          `mapstructure:"max_query_sample_count"`
	TopQueryCount       uint          `mapstructure:"top_query_count"`
	CollectionInterval  time.Duration `mapstructure:"collection_interval"`

	// QueryPlanCacheSize is the maximum number of query plans to cache in memory.
	// Caching reduces database load by avoiding repeated fetches of the same plan.
	// Set to 0 to disable caching.
	QueryPlanCacheSize int `mapstructure:"query_plan_cache_size"`

	// QueryPlanCacheTTL is how long to keep query plans in the cache.
	// Plans older than this will be evicted and refetched on next collection.
	QueryPlanCacheTTL time.Duration `mapstructure:"query_plan_cache_ttl"`

	// MaxQueryPlanSize is the maximum size in bytes for a query plan after compression.
	// Plans exceeding this size will be truncated. Set to 0 for no limit.
	// This is a safety net for extremely large plans.
	// Note: Query plans are always compressed using gzip to fit CloudWatch Logs 1MB limit.
	MaxQueryPlanSize int `mapstructure:"max_query_plan_size"`
}

// Config defines configuration for a sqlserver receiver.
type Config struct {
	scraperhelper.ControllerConfig `mapstructure:",squash"`
	metadata.MetricsBuilderConfig  `mapstructure:",squash"`
	metadata.LogsBuilderConfig     `mapstructure:",squash"`
	// EnableTopQueryCollection enables the collection of the top queries by the execution time.
	// It will collect the top N queries based on totalElapsedTimeDiffs during the last collection interval.
	// The query statement will also be reported, hence, it is not ideal to send it as a metric. Hence
	// we are reporting them as logs.
	// The `N` is configured via `TopQueryCount`
	TopQueryCollection `mapstructure:"top_query_collection"`

	QuerySample `mapstructure:"query_sample_collection"`

	InstanceName string `mapstructure:"instance_name"`
	ComputerName string `mapstructure:"computer_name"`

	DataSource string `mapstructure:"datasource"`

	Password configopaque.String `mapstructure:"password"`
	Passfile string              `mapstructure:"passfile"`
	Port     uint                `mapstructure:"port"`
	Server   string              `mapstructure:"server"`
	Username string              `mapstructure:"username"`

	// Flag to check if the connection is direct or not. It should only be
	// used after a successful call to the `Validate` method.
	isDirectDBConnectionEnabled bool
}

func (cfg *Config) Validate() error {
	err := cfg.validateInstanceAndComputerName()
	if err != nil {
		return err
	}

	if cfg.LookbackTime < 0 {
		return errors.New("lookback_time cannot have negative values")
	}

	if cfg.MaxQuerySampleCount > 10000 {
		return errors.New("`max_query_sample_count` must be between 0 and 10000")
	}

	if cfg.TopQueryCount > cfg.MaxQuerySampleCount {
		return errors.New("`top_query_count` must be less than or equal to `max_query_sample_count`")
	}

	if cfg.TopQueryCollection.CollectionInterval < 0 {
		return errors.New("`top_query_collection.collection_interval` must not be less than 0")
	}

	cfg.isDirectDBConnectionEnabled, err = directDBConnectionEnabled(cfg)
	if err != nil {
		return err
	}

	// When a password is set it takes priority over the passfile, so only the
	// passfile permissions need to be validated when no inline password is set.
	if cfg.isDirectDBConnectionEnabled && string(cfg.Password) == "" && cfg.Passfile != "" {
		if err := cfg.validatePassfilePermissions(); err != nil {
			return err
		}
	}

	return nil
}

func directDBConnectionEnabled(config *Config) (bool, error) {
	credentialPresent := string(config.Password) != "" || config.Passfile != ""

	noneOfServerUserPasswordPortSet := config.Server == "" && config.Username == "" && !credentialPresent && config.Port == 0
	if config.DataSource == "" && noneOfServerUserPasswordPortSet {
		// If no connection information is provided, we can't connect directly and this is a valid config.
		return false, nil
	}

	anyOfServerUserPasswordPortSet := config.Server != "" || config.Username != "" || credentialPresent || config.Port != 0
	if config.DataSource != "" && anyOfServerUserPasswordPortSet {
		return false, errors.New("wrong config: when specifying 'datasource' no other connection parameters ('server', 'username', 'password', 'passfile', or 'port') should be set")
	}

	if config.DataSource == "" && (config.Server == "" || config.Username == "" || !credentialPresent || config.Port == 0) {
		return false, errors.New("wrong config: when specifying either 'server', 'username', 'password' (or 'passfile'), or 'port' all of them need to be specified")
	}

	// It is a valid direct connection configuration
	return true, nil
}

func (cfg *Config) EffectiveLookbackTime() time.Duration {
	if cfg.LookbackTime == 0 {
		return 2 * cfg.TopQueryCollection.CollectionInterval
	}
	return cfg.LookbackTime
}
