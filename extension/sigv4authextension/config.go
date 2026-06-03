// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension"

import (
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"go.opentelemetry.io/collector/component"
)

// Config stores the configuration for the Sigv4 Authenticator
type Config struct {
	Region                string     `mapstructure:"region,omitempty"`
	Service               string     `mapstructure:"service,omitempty"`
	Profile               string     `mapstructure:"profile,omitempty"`
	SharedCredentialsFile string     `mapstructure:"shared_credentials_file,omitempty"`
	LocalMode             bool       `mapstructure:"local_mode,omitempty"`
	AssumeRole            AssumeRole `mapstructure:"assume_role"`
	credsProvider         *aws.CredentialsProvider
}

// AssumeRole holds the configuration needed to assume a role
type AssumeRole struct {
	ARN                  string `mapstructure:"arn,omitempty"`
	SessionName          string `mapstructure:"session_name,omitempty"`
	STSRegion            string `mapstructure:"sts_region,omitempty"`
	WebIdentityTokenFile string `mapstructure:"web_identity_token_file,omitempty"`
}

// compile time check that the Config struct satisfies the component.Config interface
var _ component.Config = (*Config)(nil)

// Validate checks that the configuration is well-formed.
func (cfg *Config) Validate() error {
	if cfg.AssumeRole.WebIdentityTokenFile != "" && cfg.AssumeRole.ARN == "" {
		return errors.New("must specify ARN when using WebIdentityTokenFile")
	}
	return nil
}

// setDefaults applies default values to the configuration. Called at extension creation,
// before any consumer reads the config.
func (cfg *Config) setDefaults() {
	if cfg.AssumeRole.STSRegion == "" && cfg.Region != "" {
		cfg.AssumeRole.STSRegion = cfg.Region
	}
}
