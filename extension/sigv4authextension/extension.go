// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension"

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	sigv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionauth"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

// sigv4Auth is a struct that implements the extensionauth.HTTPClient interface.
// It provides the implementation for providing Sigv4 authentication for HTTP requests only.
type sigv4Auth struct {
	cfg                    *Config
	logger                 *zap.Logger
	credsProvider          *aws.CredentialsProvider
	awsSDKInfo             string
	component.StartFunc    // embedded default behavior to do nothing with Start()
	component.ShutdownFunc // embedded default behavior to do nothing with Shutdown()
}

// compile time check that the sigv4Auth struct satisfies the extensionauth.HTTPClient interface
var (
	_ extension.Extension      = (*sigv4Auth)(nil)
	_ extensionauth.HTTPClient = (*sigv4Auth)(nil)
)

// RoundTripper() returns a custom signingRoundTripper.
func (sa *sigv4Auth) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	cfg := sa.cfg

	signer := sigv4.NewSigner()

	// Create the signingRoundTripper struct
	rt := signingRoundTripper{
		transport:     base,
		signer:        signer,
		region:        cfg.Region,
		service:       cfg.Service,
		credsProvider: sa.credsProvider,
		awsSDKInfo:    sa.awsSDKInfo,
		logger:        sa.logger,
	}

	return &rt, nil
}

// newSigv4Extension returns a new sigv4Auth backed by the given credentials provider.
func newSigv4Extension(cfg *Config, credsProvider *aws.CredentialsProvider, awsSDKInfo string, logger *zap.Logger) *sigv4Auth {
	return &sigv4Auth{
		cfg:           cfg,
		credsProvider: credsProvider,
		logger:        logger,
		awsSDKInfo:    awsSDKInfo,
	}
}

// resolveCredentialsProvider builds an aws.CredentialsProvider by delegating to awsutilv2.GetAWSConfig
// which handles shared credentials, web identity, and assume-role.
func resolveCredentialsProvider(ctx context.Context, logger *zap.Logger, cfg *Config) (*aws.CredentialsProvider, error) {
	settings := cfg.AWSSessionSettings
	settings.Region = cfg.resolvedSTSRegion()
	settings.RoleARN = cfg.resolvedRoleARN()
	settings.WebIdentityTokenFile = cfg.resolvedWebIdentityTokenFile()
	awscfg, err := awsutilv2.GetAWSConfig(ctx, logger, &settings)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve credentials provider: %w", err)
	}
	// Skip eager Retrieve for web identity: the token may not be available yet at startup
	// (e.g., projected SA token in Kubernetes) and will be read on first use.
	if settings.WebIdentityTokenFile == "" {
		if _, err = awscfg.Credentials.Retrieve(ctx); err != nil {
			return nil, fmt.Errorf("could not retrieve credentials: %w", err)
		}
	}
	return &awscfg.Credentials, nil
}
