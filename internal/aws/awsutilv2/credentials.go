// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"context"
	"os"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"
	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/ec2rolecreds"
	"go.uber.org/zap"
)

// loadConfig calls the supplied load function with options for the given region, credentials provider,
// and HTTP client. Retries once on failure after retryDelay. Then eagerly retrieves credentials so the
// source can be logged and an IMDS-fallback warning surfaced when applicable.
//
// A successful return does not guarantee credentials are valid. The early Retrieve is for logging only.
// Lazy retry happens on actual SDK API calls.
func loadConfig(ctx context.Context, logger *zap.Logger, settings *AWSSessionSettings, region string, provider aws.CredentialsProvider, httpClient *awshttp.BuildableClient, retryDelay time.Duration, load loadConfigFn) (aws.Config, error) {
	cfgFiles := getFallbackSharedConfigFiles(backwardsCompatibleUserHomeDir)
	logger.Debug("Fallback shared config file(s)", zap.Strings("files", cfgFiles))

	opts := buildLoadOptions(settings, region, cfgFiles, httpClient, provider)

	cfg, err := load(ctx, opts...)
	if err != nil {
		logger.Error("Failed to create credential sessions, retrying", zap.Duration("delay", retryDelay), zap.Error(err))
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return aws.Config{}, ctx.Err()
		}
		cfg, err = load(ctx, opts...)
		if err != nil {
			logger.Error("Retry failed for creating credential sessions", zap.Error(err))
			return aws.Config{}, err
		}
	}

	cred, retrieveErr := cfg.Credentials.Retrieve(ctx)
	if retrieveErr != nil {
		logger.Error("Failed to get credential from session", zap.Error(retrieveErr))
	} else {
		logger.Debug("Using credential", zap.String("access-key", cred.AccessKeyID), zap.String("source", cred.Source))
		if cred.Source == ec2rolecreds.ProviderName {
			warnIfUnusedSharedConfigFiles(logger)
		}
	}
	return cfg, nil
}

// warnIfUnusedSharedConfigFiles logs a warning when shared config files exist in the current user's home
// directory but the active credentials came from IMDS. The user may have intended for those files to be used.
func warnIfUnusedSharedConfigFiles(logger *zap.Logger) {
	var found []string
	for _, cfgFile := range getFallbackSharedConfigFiles(currentUserHomeDir) {
		if _, err := os.Stat(cfgFile); err == nil {
			found = append(found, cfgFile)
		}
	}
	if len(found) > 0 {
		logger.Warn("Unused shared config file(s) found", zap.Strings("files", found))
	}
}

// rootCredentialsProvider returns the first non-nil entry in the credentials chain. Later entries
// are never consulted at runtime. Returns nil if the chain is empty, signaling the caller to use
// the SDK default chain.
func rootCredentialsProvider(settings *AWSSessionSettings, factories []awsv2.CredentialsProviderFactory) aws.CredentialsProvider {
	for _, p := range buildCredentialProviderChain(settings, factories) {
		if p != nil {
			return p
		}
	}
	return nil
}

// buildCredentialProviderChain returns providers in priority order: override factories per file, then a
// profile-only refreshable when no shared file is configured, then a per-file refreshable for each
// configured shared file.
func buildCredentialProviderChain(settings *AWSSessionSettings, factories []awsv2.CredentialsProviderFactory) []aws.CredentialsProvider {
	var chain []aws.CredentialsProvider

	for _, factory := range factories {
		for _, file := range settings.SharedCredentialsFile {
			if p := factory(file); p != nil {
				chain = append(chain, ensureCached(p))
			}
		}
	}

	if settings.Profile != "" && len(settings.SharedCredentialsFile) == 0 {
		chain = append(chain, ensureCached(
			NewRefreshableSharedCredentialsProvider("", settings.Profile, defaultExpiryWindow),
		))
	}

	for _, file := range settings.SharedCredentialsFile {
		chain = append(chain, ensureCached(
			NewRefreshableSharedCredentialsProvider(file, settings.Profile, defaultExpiryWindow),
		))
	}

	return chain
}

// ensureCached wraps the provider in aws.NewCredentialsCache if it is not already wrapped.
func ensureCached(p aws.CredentialsProvider) aws.CredentialsProvider {
	if _, ok := p.(*aws.CredentialsCache); ok {
		return p
	}
	return aws.NewCredentialsCache(p)
}

// buildLoadOptions assembles the SDK LoadOptions used by loadConfig.
func buildLoadOptions(settings *AWSSessionSettings, region string, cfgFiles []string, httpClient *awshttp.BuildableClient, provider aws.CredentialsProvider) []func(*config.LoadOptions) error {
	// v2 SDK's RetryMaxAttempts counts the initial attempt. The +1 keeps the v1 contract
	// where MaxRetries=N means N retries beyond the initial. Negative values clamp to 0.
	retries := max(settings.MaxRetries, 0)
	opts := []func(*config.LoadOptions) error{
		config.WithHTTPClient(httpClient),
		config.WithRetryMaxAttempts(retries + 1),
		config.WithSharedCredentialsFiles(cfgFiles),
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if settings.Endpoint != "" {
		opts = append(opts, config.WithBaseEndpoint(settings.Endpoint))
	}
	if provider != nil {
		opts = append(opts, config.WithCredentialsProvider(provider))
	}
	return opts
}
