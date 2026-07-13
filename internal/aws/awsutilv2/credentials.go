// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"
	"github.com/aws/aws-sdk-go-v2/aws"
)

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
