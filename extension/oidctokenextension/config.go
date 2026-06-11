// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"

import (
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
)

// ProviderType selects the OIDC token provider.
type ProviderType string

const (
	// ProviderAuto detects the provider from the environment.
	ProviderAuto ProviderType = "auto"
	// ProviderAzure uses Azure VM managed identity (IMDS) to fetch tokens.
	ProviderAzure ProviderType = "azure"
	// ProviderNone disables token fetching.
	ProviderNone ProviderType = "none"
)

func (p *ProviderType) UnmarshalText(text []byte) error {
	switch v := ProviderType(text); v {
	case ProviderAuto, ProviderAzure, ProviderNone:
		*p = v
		return nil
	default:
		return fmt.Errorf("unsupported provider %q (expected auto, azure, or none)", v)
	}
}

func (p ProviderType) MarshalText() ([]byte, error) {
	return []byte(p), nil
}

// Config defines the configuration for the OIDC token extension.
type Config struct {
	// Provider selects the OIDC token provider. Defaults to "auto" which
	// detects the provider from the environment. Set to "none" to disable.
	Provider ProviderType `mapstructure:"provider,omitempty"`

	// Audience is the audience/resource claim requested in the OIDC token.
	// Each provider has its own default if not set.
	Audience string `mapstructure:"audience,omitempty"`

	// OutputTokenFile is the path where the extension writes the fetched OIDC
	// token. Point sigv4auth's web_identity_token_file to the same path.
	OutputTokenFile string `mapstructure:"output_token_file"`
}

var _ component.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.OutputTokenFile == "" {
		return errors.New("output_token_file is required")
	}
	return nil
}

func (c *Config) buildProviders() []TokenProvider {
	switch c.Provider {
	case ProviderNone:
		return nil
	case ProviderAzure:
		return []TokenProvider{newAzureProvider(c.Audience)}
	default: // ProviderAuto
		return []TokenProvider{newAzureProvider(c.Audience)}
	}
}
