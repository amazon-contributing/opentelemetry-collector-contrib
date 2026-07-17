// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/awscloudwatchlogsprovisionerextension"

import (
	"errors"
	"time"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

// Config for the awscloudwatchlogsprovisioner extension.
//
// The extension reads x-aws-log-group and x-aws-log-stream headers from outgoing
// HTTP requests and lazily creates the corresponding CloudWatch log groups and
// streams. Headers can be set by the otlphttp exporter (static) or by the
// headers_setter extension (dynamic, from client.Metadata).
type Config struct {
	awsutilv2.AWSSessionSettings `mapstructure:",squash"`

	// AdditionalAuth is a reference to the inner auth extension (typically sigv4auth)
	// that this extension chains with for request signing. Follows the same pattern
	// as headers_setter's additional_auth field.
	AdditionalAuth *component.ID `mapstructure:"additional_auth"`

	// LogsProvisionFailureBackoff is the TTL for negative cache entries.
	// During this period, the extension won't retry creation for the same (group, stream) pair.
	// Default: 5s.
	LogsProvisionFailureBackoff time.Duration `mapstructure:"logs_provision_failure_backoff"`
}

func (cfg *Config) Validate() error {
	if cfg.Region == "" {
		return errors.New("region is required")
	}
	if cfg.LogsProvisionFailureBackoff < 0 {
		return errors.New("logs_provision_failure_backoff must not be negative")
	}
	return nil
}
