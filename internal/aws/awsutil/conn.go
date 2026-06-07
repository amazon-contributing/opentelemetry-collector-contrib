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
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"go.uber.org/zap"
)

// GetAWSConfig returns an aws.Config configured per AWSSessionSettings.
//
// Region resolution priority: settings.Region, then AWS_REGION, then EC2
// IMDS (skipped when settings.LocalMode is true). When settings.RoleARN
// is non-empty, credentials are wrapped in an STS AssumeRole provider
// that falls back from the regional endpoint to the partition's primary
// endpoint on RegionDisabledException. Otherwise credentials come from
// the chain in getCredentialProviderChain, falling through to the SDK
// default chain when that's empty.
//
// settings is read-only; the same pointer can be reused across calls.
func GetAWSConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings) (aws.Config, error) {
	return getAWSConfig(ctx, logger, settings)
}

func getAWSConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings) (aws.Config, error) {
	httpClient, err := newHTTPClient(
		logger,
		settings.NumberOfWorkers,
		settings.RequestTimeoutSeconds,
		settings.NoVerifySSL,
		settings.ProxyAddress,
		settings.CertificateFilePath,
	)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return aws.Config{}, err
	}

	region, err := resolveRegion(ctx, logger, settings, httpClient)
	if err != nil {
		return aws.Config{}, err
	}
	if region == "" {
		msg := "Cannot fetch region variable from config file, environment variables and ec2 metadata."
		logger.Error(msg)
		return aws.Config{}, errors.New(msg)
	}

	provider := getRootCredentials(settings)

	cfg, err := loadConfig(ctx, logger, region, provider, httpClient)
	if err != nil {
		return aws.Config{}, err
	}

	// SDK v2's RetryMaxAttempts counts the initial attempt; v1's MaxRetries
	// did not. The +1 keeps the v1 caller contract.
	cfg.RetryMaxAttempts = settings.MaxRetries + 1
	if settings.Endpoint != "" {
		cfg.BaseEndpoint = aws.String(settings.Endpoint)
	}

	if settings.RoleARN != "" {
		cfg.Credentials = aws.NewCredentialsCache(
			newStsCredentialsProvider(cfg, settings.RoleARN, region, settings.ExternalID),
		)
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
	httpClient aws.HTTPClient,
) (string, error) {
	if settings.Region != "" {
		logger.Debug("Fetch region from commandline/config file", zap.String("region", settings.Region))
		return settings.Region, nil
	}
	if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
		logger.Debug("Fetch region from environment variables", zap.String("region", envRegion))
		return envRegion, nil
	}
	if settings.LocalMode {
		return "", nil
	}

	region, err := resolveRegionFromIMDS(ctx, logger, settings.IMDSRetries, httpClient)
	if err != nil {
		logger.Error("Unable to retrieve the region from the EC2 instance", zap.Error(err))
		return "", nil
	}
	logger.Debug("Fetch region from ec2 metadata", zap.String("region", region))
	return region, nil
}

// resolveRegionFromIMDS resolves the region via EC2 IMDS using the shared
// strict-then-permissive client from override/aws: an IMDSv2-only client
// (with the IMDS retryer) is tried first, falling back to a permissive client
// (IMDSv1 fallback enabled) on failure. The supplied httpClient flows through
// to both underlying clients so per-component TLS / proxy / cert-pool config
// applies to IMDS too.
func resolveRegionFromIMDS(
	ctx context.Context,
	logger *zap.Logger,
	retries int,
	httpClient aws.HTTPClient,
) (string, error) {
	client := override.NewIMDSClient(logger, retries, func(o *imds.Options) {
		o.HTTPClient = httpClient
	})
	out, err := client.GetRegion(ctx, &imds.GetRegionInput{})
	if err != nil {
		return "", err
	}
	return out.Region, nil
}
