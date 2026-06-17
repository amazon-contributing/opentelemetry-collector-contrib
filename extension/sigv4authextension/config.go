// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension"

import (
	"errors"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

// Config stores the configuration for the Sigv4 Authenticator
type Config struct {
	awsutilv2.AWSSessionSettings `mapstructure:",squash"`

	Service    string     `mapstructure:"service,omitempty"`
	AssumeRole AssumeRole `mapstructure:"assume_role"`
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
	if cfg.AssumeRole.ARN != "" && cfg.RoleARN != "" {
		return errors.New("role_arn and assume_role.arn cannot both be set")
	}
	if cfg.AssumeRole.WebIdentityTokenFile != "" && cfg.resolvedRoleARN() == "" {
		return errors.New("must specify role_arn or assume_role.arn when using WebIdentityTokenFile")
	}
	return nil
}

// resolvedRoleARN returns whichever of cfg.RoleARN (top-level) or cfg.AssumeRole.ARN is set.
// Validate guarantees they are not both set; returns "" when neither is set.
func (cfg *Config) resolvedRoleARN() string {
	if cfg.AssumeRole.ARN != "" {
		return cfg.AssumeRole.ARN
	}
	return cfg.RoleARN
}

// resolvedSTSRegion returns AssumeRole.STSRegion if set, otherwise falls back to Region.
func (cfg *Config) resolvedSTSRegion() string {
	if cfg.AssumeRole.STSRegion != "" {
		return cfg.AssumeRole.STSRegion
	}
	return cfg.Region
}
