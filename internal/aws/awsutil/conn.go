// Copyright The OpenTelemetry Authors
// Portions of this file Copyright 2018-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"context"
	"errors"
	"os"

	override "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/ec2rolecreds"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"go.uber.org/zap"
)

// GetAWSConfig returns an aws.Config configured per AWSSessionSettings.
//
// Region resolution priority: settings.Region, then AWS_REGION, then EC2
// IMDS (skipped when settings.LocalMode is true).
//
// Credential wrapping (over the resolved base credentials, with regional->
// partitional STS fallback on RegionDisabledException):
//   - settings.WebIdentityTokenFile set: STS AssumeRoleWithWebIdentity using that
//     OIDC token file (requires settings.RoleARN; the token is read lazily on
//     first Retrieve, so a not-yet-present projected token does not fail here).
//   - else settings.RoleARN set: STS AssumeRole (threading settings.ExternalID
//     when non-empty).
//   - else: the base chain from getRootCredentials, falling through to the SDK
//     default chain when that's empty.
//
// settings is read-only; the same pointer can be reused across calls.
func GetAWSConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings) (aws.Config, error) {
	return getAWSConfig(ctx, logger, settings)
}

func getAWSConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings) (aws.Config, error) {
	httpClient, err := getHTTPClient(logger, settings)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return aws.Config{}, err
	}

	region := resolveRegion(ctx, logger, settings)
	if region == "" {
		msg := "Cannot fetch region variable from config file, environment variables and ec2 metadata."
		if settings.LocalMode {
			msg = "region is required when local_mode is enabled"
		}
		logger.Error(msg)
		return aws.Config{}, errors.New(msg)
	}

	provider := getRootCredentials(settings)

	cfg, err := loadConfig(ctx, logger, region, provider)
	if err != nil {
		return aws.Config{}, err
	}

	switch {
	case settings.WebIdentityTokenFile != "":
		if settings.RoleARN == "" {
			return aws.Config{}, errors.New("role_arn must be set when web_identity_token_file is configured")
		}
		cfg.Credentials = aws.NewCredentialsCache(
			newWebIdentityCredentialsProvider(cfg, settings.RoleARN, region,
				stscreds.IdentityTokenFile(settings.WebIdentityTokenFile)),
		)
		logger.Debug("Using web identity credentials provider")
	default:
		// Eagerly Retrieve on the base chain for the diagnostic log of the
		// resolved credential source. Skipped for web identity because the base
		// chain is unused there: on hosts without EC2 IMDS (e.g. Azure VMs) it
		// always fails and produces a spurious ERROR at startup.
		cred, retrieveErr := cfg.Credentials.Retrieve(ctx)
		if retrieveErr != nil {
			logger.Error("Failed to get credential from session", zap.Error(retrieveErr))
		}
		if settings.RoleARN != "" {
			cfg.Credentials = aws.NewCredentialsCache(
				newAssumeRoleCredentialsProvider(cfg, settings.RoleARN, region, settings.ExternalID),
			)
			logger.Debug("Using assume role credentials provider")
		} else if retrieveErr == nil {
			logger.Debug("Using credential from session",
				zap.String("access-key", cred.AccessKeyID),
				zap.String("source", cred.Source))
			if cred.Source == ec2rolecreds.ProviderName {
				warnIfUnusedSharedConfigFiles(logger)
			}
		}
	}

	// Keep these mutations after the credential-provider construction above:
	// sts.NewFromConfig snapshots the config, so setting them earlier would
	// give the STS credential clients the data-plane endpoint, retry budget,
	// and custom HTTP client. The custom client (proxy_address,
	// certificate_file_path, no_verify_ssl, request_timeout_seconds) is
	// scoped to data-plane clients only;
	// IMDS, the default credential chain, and STS use SDK default clients.
	cfg.HTTPClient = httpClient
	cfg.RetryMaxAttempts = max(settings.MaxRetries, 0) + 1
	if settings.Endpoint != "" {
		cfg.BaseEndpoint = aws.String(settings.Endpoint)
	}

	return cfg, nil
}

// resolveRegion returns the region from settings.Region, AWS_REGION, or
// EC2 IMDS in that order. When LocalMode is true, IMDS is skipped and the
// empty string is returned for the caller to surface as an error.
func resolveRegion(
	ctx context.Context,
	logger *zap.Logger,
	settings *AWSSessionSettings,
) string {
	if settings.Region != "" {
		logger.Debug("Fetch region from commandline/config file", zap.String("region", settings.Region))
		return settings.Region
	}
	if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
		logger.Debug("Fetch region from environment variables", zap.String("region", envRegion))
		return envRegion
	}
	if settings.LocalMode {
		return ""
	}

	region, err := resolveRegionFromIMDS(ctx, logger, settings.IMDSRetries)
	if err != nil {
		logger.Error("Unable to retrieve the region from the EC2 instance", zap.Error(err))
		return ""
	}
	logger.Debug("Fetch region from ec2 metadata", zap.String("region", region))
	return region
}

// imdsRegionClient is the subset of *override.IMDSClient used for region
// lookup; newIMDSClient is overrideable in tests.
type imdsRegionClient interface {
	GetRegion(ctx context.Context, params *imds.GetRegionInput, optFns ...func(*imds.Options)) (*imds.GetRegionOutput, error)
}

var newIMDSClient = func(logger *zap.Logger, retries int, optFns ...func(*imds.Options)) imdsRegionClient {
	return override.NewIMDSClient(logger, retries, optFns...)
}

// resolveRegionFromIMDS resolves the region via EC2 IMDS using the shared
// strict-then-permissive client from override/aws: an IMDSv2-only client
// (with the IMDS retryer) is tried first, falling back to a permissive client
// (IMDSv1 fallback enabled) on failure. Both underlying clients use the SDK
// default IMDS HTTP client (fast-fail timeouts); the component's custom
// client is data-plane only.
func resolveRegionFromIMDS(
	ctx context.Context,
	logger *zap.Logger,
	retries int,
) (string, error) {
	client := newIMDSClient(logger, retries)
	out, err := client.GetRegion(ctx, &imds.GetRegionInput{})
	if err != nil {
		return "", err
	}
	return out.Region, nil
}
