// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"go.uber.org/zap"
)

const (
	// initialLoadRetryDelay is the wait between LoadDefaultConfig retries.
	initialLoadRetryDelay = 15 * time.Second
	envAwsRegion          = "AWS_REGION"
)

// loadConfigFn matches config.LoadDefaultConfig and is overridable in tests.
type loadConfigFn func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error)

// GetAWSConfig returns an aws.Config built from settings.
//
// Region resolution priority:
//  1. settings.Region
//  2. AWS_REGION environment variable
//  3. EC2 IMDS (skipped when settings.LocalMode is true)
//
// Credential resolution chain (first non-nil wins, later entries are not consulted):
//  1. Override factories registered via override/awsv2.GetCredentialsChainOverride().
//  2. A refreshable shared credentials provider, built from settings.Profile and
//     settings.SharedCredentialsFile when either is set.
//  3. The SDK default chain (env vars, shared config, IMDS) otherwise.
//
// When settings.RoleARN is set, the result is wrapped with regional/partitional STS assume-role.
// The regional endpoint is tried first; on RegionDisabledException, the partition's primary
// endpoint is used (cached for subsequent calls). When AMZ_SOURCE_ARN and AMZ_SOURCE_ACCOUNT
// are both set in the environment, confused-deputy headers are injected on assume-role calls.
//
// Returns an error if the HTTP client cannot be built, the region cannot be resolved, or the
// initial config load fails after a retry.
func GetAWSConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings) (aws.Config, error) {
	return getAWSConfig(ctx, logger, settings, initialLoadRetryDelay, config.LoadDefaultConfig)
}

// getAWSConfig is the orchestration with retryDelay and the LoadDefaultConfig function as injectable
// parameters so tests can shorten the retry wait and simulate load failures.
func getAWSConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings, retryDelay time.Duration, load loadConfigFn) (aws.Config, error) {
	httpClient, err := buildHTTPClient(logger, settings)
	if err != nil {
		logger.Error("Failed to build HTTP client", zap.Error(err))
		return aws.Config{}, err
	}

	region, err := resolveRegion(ctx, logger, settings, httpClient)
	if err != nil {
		logger.Error("Failed to resolve region", zap.Error(err))
		return aws.Config{}, err
	}

	provider := rootCredentialsProvider(settings, awsv2.GetCredentialsChainOverride().GetCredentialsChain())

	cfg, err := loadConfig(ctx, logger, settings, region, provider, httpClient, retryDelay, load)
	if err != nil {
		return aws.Config{}, err
	}

	if settings.RoleARN != "" {
		cfg.Credentials = aws.NewCredentialsCache(
			newStsCredentialsProvider(cfg, settings.RoleARN, region, settings.ExternalID),
		)
	}

	return cfg, nil
}

// resolveRegion determines the AWS region in priority order: settings.Region, AWS_REGION env var, then IMDS.
// LocalMode short-circuits before IMDS. Returns an error when no source produces a region.
func resolveRegion(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings, httpClient aws.HTTPClient) (string, error) {
	if settings.Region != "" {
		logger.Debug("Region fetched from config", zap.String("region", settings.Region))
		return settings.Region, nil
	}
	if envRegion := os.Getenv(envAwsRegion); envRegion != "" {
		logger.Debug("Region fetched from environment variable", zap.String("region", envRegion))
		return envRegion, nil
	}
	if settings.LocalMode {
		return "", errors.New("region is required when local_mode is enabled")
	}

	region, err := resolveRegionFromIMDS(ctx, logger, settings.IMDSRetries, httpClient)
	if err != nil {
		return "", fmt.Errorf("failed to resolve region from EC2 metadata: %w", err)
	}
	logger.Debug("Region fetched from EC2 metadata", zap.String("region", region))
	return region, nil
}

// resolveRegionFromIMDS tries IMDSv2 strictly (no fallback to v1) with the configured retryer. On failure,
// retries with a permissive client that allows IMDSv1 fallback and uses the SDK default retryer.
func resolveRegionFromIMDS(ctx context.Context, logger *zap.Logger, retries int, httpClient aws.HTTPClient) (string, error) {
	region, err := getRegionFromIMDS(ctx, imds.Options{
		HTTPClient:     httpClient,
		Retryer:        awsv2.NewIMDSRetryer(retries),
		EnableFallback: aws.FalseTernary,
	})
	if err == nil {
		return region, nil
	}
	logger.Debug("IMDSv2 strict region lookup failed, falling back to permissive client", zap.Error(err))

	return getRegionFromIMDS(ctx, imds.Options{
		HTTPClient:     httpClient,
		EnableFallback: aws.TrueTernary,
	})
}

// getRegionFromIMDS is overrideable in tests.
var getRegionFromIMDS = getIMDSRegion

func getIMDSRegion(ctx context.Context, opts imds.Options) (string, error) {
	out, err := imds.New(opts).GetRegion(ctx, &imds.GetRegionInput{})
	if err != nil {
		return "", err
	}
	return out.Region, nil
}
