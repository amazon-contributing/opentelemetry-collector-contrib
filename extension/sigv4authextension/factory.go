// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension"

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension/internal/metadata"
)

// NewFactory creates a factory for the Sigv4 Authenticator extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(metadata.Type, createDefaultConfig, createExtension, metadata.ExtensionStability)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createExtension(ctx context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	c := cfg.(*Config)
	credsProvider, err := resolveCredentialsProvider(ctx, set.Logger, c)
	if err != nil {
		return nil, err
	}
	awsSDKInfo := fmt.Sprintf("%s/%s", aws.SDKName, aws.SDKVersion)
	return newSigv4Extension(c, credsProvider, awsSDKInfo, set.Logger), nil
}
