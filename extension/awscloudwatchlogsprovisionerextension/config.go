// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/awscloudwatchlogsprovisionerextension"

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"
)

// Config for the awscloudwatchlogsprovisioner extension.
//
// The extension reads x-aws-log-group and x-aws-log-stream headers from outgoing
// HTTP requests and lazily creates the corresponding CloudWatch log groups and
// streams. Headers can be set by the otlphttp exporter (static) or by the
// headers_setter extension (dynamic, from client.Metadata).
type Config struct {
	// Region is the AWS region for CloudWatch Logs API calls (required).
	Region string `mapstructure:"region"`

	// AdditionalAuth is a reference to the inner auth extension (typically sigv4auth)
	// that this extension chains with for request signing. Follows the same pattern
	// as headers_setter's additional_auth field.
	AdditionalAuth *component.ID `mapstructure:"additional_auth"`

	// LogsProvisionTimeout is the HTTP timeout for each CreateLogGroup/CreateLogStream
	// API call (including SDK retries). Bounds how long singleflight waiters block.
	// Default: 10s.
	LogsProvisionTimeout time.Duration `mapstructure:"logs_provision_timeout"`

	// LogsProvisionFailureBackoff is the TTL for negative cache entries.
	// During this period, the extension won't retry creation for the same (group, stream) pair.
	// Default: 30s.
	LogsProvisionFailureBackoff time.Duration `mapstructure:"logs_provision_failure_backoff"`
}

func (cfg *Config) Validate() error {
	if cfg.Region == "" {
		return errors.New("region is required")
	}
	if cfg.LogsProvisionTimeout <= 0 {
		return errors.New("logs_provision_timeout must be positive")
	}
	if cfg.LogsProvisionFailureBackoff < 0 {
		return errors.New("logs_provision_failure_backoff must not be negative")
	}
	return nil
}
