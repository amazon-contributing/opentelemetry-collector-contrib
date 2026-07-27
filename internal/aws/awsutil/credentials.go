// Copyright The OpenTelemetry Authors
// Portions of this file Copyright 2018-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"context"
	"os"
	"time"

	override "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"go.uber.org/zap"
)

// initialLoadRetryDelay shields agent startup from transient
// credentials-file-mount races on Kubernetes / ECS by sleeping once before
// retrying a failed config load.
const initialLoadRetryDelay = 15 * time.Second

// getCredentialProviderChain returns providers in this order:
//
//  1. override factories from override/aws.GetCredentialsChainOverride(),
//     invoked once per cfg.SharedCredentialsFile entry. Empty when
//     SharedCredentialsFile is empty.
//  2. profile-only fallback when cfg.Profile is set and
//     SharedCredentialsFile is empty.
//  3. one entry per cfg.SharedCredentialsFile, all using cfg.Profile.
//
// Each refreshable entry is wrapped in aws.NewCredentialsCache so the
// 10-minute expiry window takes effect.
//
// Explicit AccessKey/SecretKey credentials are not supported here —
// AWSSessionSettings does not expose those fields. If neither Profile
// nor SharedCredentialsFile is set and no override is registered, the
// caller lets config.LoadDefaultConfig resolve via the SDK default chain.
func getCredentialProviderChain(cfg *AWSSessionSettings) []aws.CredentialsProvider {
	var chain []aws.CredentialsProvider

	for _, factory := range override.GetCredentialsChainOverride().GetCredentialsChain() {
		for _, file := range cfg.SharedCredentialsFile {
			if p := factory(file); p != nil {
				chain = append(chain, p)
			}
		}
	}

	if cfg.Profile != "" && len(cfg.SharedCredentialsFile) == 0 {
		chain = append(chain, aws.NewCredentialsCache(RefreshableSharedCredentialsProvider{
			Provider:     SharedCredentialsProvider{Filename: "", Profile: cfg.Profile},
			ExpiryWindow: defaultExpiryWindow,
		}))
	}

	for _, file := range cfg.SharedCredentialsFile {
		chain = append(chain, aws.NewCredentialsCache(RefreshableSharedCredentialsProvider{
			Provider:     SharedCredentialsProvider{Filename: file, Profile: cfg.Profile},
			ExpiryWindow: defaultExpiryWindow,
		}))
	}

	return chain
}

// getRootCredentials returns the first non-nil entry in the chain.
// First-non-nil pick, not a fallback chain — later entries are never
// consulted at runtime. Returns nil if the chain is empty, signaling the
// caller to let the SDK default chain resolve credentials.
func getRootCredentials(cfg *AWSSessionSettings) aws.CredentialsProvider {
	for _, p := range getCredentialProviderChain(cfg) {
		if p != nil {
			return p
		}
	}
	return nil
}

// loadConfig invokes config.LoadDefaultConfig with the project-pinned
// shared-credentials file list and an optional credentials provider.
// Sleeps initialLoadRetryDelay and retries once on initial failure.
//
// loadConfig does not eagerly Retrieve credentials — that decision is left
// to the caller so it can be gated on settings.WebIdentityTokenFile (where
// the base chain is intentionally unused and would produce spurious errors,
// e.g. on Azure VMs).
func loadConfig(
	ctx context.Context,
	logger *zap.Logger,
	region string,
	provider aws.CredentialsProvider,
	httpClient aws.HTTPClient,
) (aws.Config, error) {
	credentialsFiles, configFiles := getFallbackSharedConfigFiles(backwardsCompatibleUserHomeDir)
	logger.Debug("Fallback shared config file(s)",
		zap.Strings("credentials", credentialsFiles),
		zap.Strings("config", configFiles))

	opts := []func(*config.LoadOptions) error{
		config.WithHTTPClient(httpClient),
		config.WithSharedCredentialsFiles(credentialsFiles),
		config.WithSharedConfigFiles(configFiles),
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if provider != nil {
		opts = append(opts, config.WithCredentialsProvider(provider))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		logger.Error("Error in creating session object waiting 15 seconds", zap.Error(err))
		time.Sleep(initialLoadRetryDelay)
		cfg, err = config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			logger.Error("Retry failed for creating credential sessions", zap.Error(err))
			return aws.Config{}, err
		}
	}

	return cfg, nil
}

// warnIfUnusedSharedConfigFiles logs a warning when shared config files
// exist under the current user's home directory but the resolved
// credentials came from a source that ignores them (e.g. EC2 IMDS).
// Called by GetAWSConfig for the base-chain path.
func warnIfUnusedSharedConfigFiles(logger *zap.Logger) {
	var found []string
	credFiles, cfgFiles := getFallbackSharedConfigFiles(currentUserHomeDir)
	for _, f := range append(credFiles, cfgFiles...) {
		if _, err := os.Stat(f); err == nil {
			found = append(found, f)
		}
	}
	if len(found) > 0 {
		logger.Warn("Unused shared config file(s) found.", zap.Strings("files", found))
	}
}
