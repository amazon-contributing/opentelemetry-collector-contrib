// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/sigv4authextension"

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	sigv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
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

// resolveCredentialsProvider dispatches to the appropriate credential source (web-identity or
// the shared-credentials chain) and returns the resolved provider.
func resolveCredentialsProvider(ctx context.Context, logger *zap.Logger, cfg *Config) (*aws.CredentialsProvider, error) {
	var (
		creds *aws.CredentialsProvider
		err   error
	)
	if cfg.AssumeRole.WebIdentityTokenFile != "" {
		creds, err = getCredsProviderFromWebIdentityConfig(ctx, logger, cfg)
	} else {
		creds, err = getCredsProviderFromConfig(ctx, logger, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("could not retrieve credential provider: %w", err)
	}
	return creds, nil
}

// getCredsProviderFromConfig builds an aws.CredentialsProvider from cfg: shared profile/file when
// configured, otherwise the SDK default chain, optionally wrapped with regional/partitional assume-role.
func getCredsProviderFromConfig(ctx context.Context, logger *zap.Logger, cfg *Config) (*aws.CredentialsProvider, error) {
	settings := awsutilv2.AWSSessionSettings{
		Region:                cfg.resolvedSTSRegion(),
		RoleARN:               cfg.AssumeRole.ARN,
		Profile:               cfg.Profile,
		LocalMode:             cfg.LocalMode,
		SharedCredentialsFile: cfg.SharedCredentialsFile,
	}
	awscfg, err := awsutilv2.GetAWSConfig(ctx, logger, &settings)
	if err != nil {
		return nil, err
	}
	if _, err = awscfg.Credentials.Retrieve(ctx); err != nil {
		return nil, err
	}
	return &awscfg.Credentials, nil
}

func getCredsProviderFromWebIdentityConfig(ctx context.Context, logger *zap.Logger, cfg *Config) (*aws.CredentialsProvider, error) {
	tokenRetriever := stscreds.IdentityTokenRetriever(
		stscreds.IdentityTokenFile(cfg.AssumeRole.WebIdentityTokenFile),
	)
	_, err := tokenRetriever.GetIdentityToken()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve token file: %w", err)
	}

	stsRegion := cfg.resolvedSTSRegion()
	awscfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithWebIdentityRoleCredentialOptions(
			func(options *stscreds.WebIdentityRoleOptions) {
				options.TokenRetriever = tokenRetriever
				options.RoleARN = cfg.AssumeRole.ARN
			},
		),
		awsconfig.WithRegion(stsRegion),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS configuration: %w", err)
	}
	stsSvc := sts.NewFromConfig(awscfg)

	provider := stscreds.NewWebIdentityRoleProvider(stsSvc, cfg.AssumeRole.ARN, tokenRetriever)
	awscfg.Credentials = aws.NewCredentialsCache(provider)
	logger.Debug("Web identity credentials provider configured",
		zap.String("role-arn", cfg.AssumeRole.ARN),
		zap.String("region", stsRegion))

	return &awscfg.Credentials, nil
}
