// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension/internal/metadata"
)

func NewFactory() extension.Factory {
	return extension.NewFactory(
		metadata.Type,
		createDefaultConfig,
		createExtension,
		metadata.ExtensionStability,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Provider: ProviderAuto,
	}
}

func createExtension(_ context.Context, settings extension.Settings, cfg component.Config) (extension.Extension, error) {
	c := cfg.(*Config)
	return &oidcTokenExtension{
		logger:             settings.Logger,
		config:             c,
		providers:          c.buildProviders(),
		minRefreshInterval: minRefreshInterval,
	}, nil
}
