// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"strings"
	"testing"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCredentialsProvider(t *testing.T) {
	testCases := map[string]struct {
		settings     *AWSSessionSettings
		expectChain  bool
		wantProvider aws.CredentialsProvider
	}{
		"Empty": {
			settings:    &AWSSessionSettings{},
			expectChain: false,
		},
		"ProfileOnly": {
			settings:     &AWSSessionSettings{Profile: testProfile},
			expectChain:  true,
			wantProvider: &refreshableSharedCredentialsProvider{},
		},
		"FilenameOnly": {
			settings:     &AWSSessionSettings{SharedCredentialsFile: []string{"F"}},
			expectChain:  true,
			wantProvider: &refreshableSharedCredentialsProvider{},
		},
		"Both": {
			settings:     &AWSSessionSettings{Profile: testProfile, SharedCredentialsFile: []string{"F"}},
			expectChain:  true,
			wantProvider: &refreshableSharedCredentialsProvider{},
		},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			provider := rootCredentialsProvider(testCase.settings, nil)
			if !testCase.expectChain {
				assert.Nil(t, provider)
				return
			}
			require.NotNil(t, provider)
			cache, ok := provider.(*aws.CredentialsCache)
			require.True(t, ok)
			assert.True(t, cache.IsCredentialsProvider(testCase.wantProvider))
		})
	}
}

func TestRootCredentialsProvider_OverrideRegistryConsulted(t *testing.T) {
	// Factory that fires only for paths starting with "OVERRIDE:".
	factories := []awsv2.CredentialsProviderFactory{
		func(file string) aws.CredentialsProvider {
			if !strings.HasPrefix(file, "OVERRIDE:") {
				return nil
			}
			return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				return aws.Credentials{AccessKeyID: "from-override-" + file, SecretAccessKey: "secret"}, nil
			})
		},
	}

	t.Run("OverrideWins", func(t *testing.T) {
		settings := &AWSSessionSettings{SharedCredentialsFile: []string{"OVERRIDE:my-id"}}
		provider := rootCredentialsProvider(settings, factories)
		require.NotNil(t, provider)

		cache, ok := provider.(*aws.CredentialsCache)
		require.True(t, ok)
		got, err := cache.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "from-override-OVERRIDE:my-id", got.AccessKeyID)
	})

	t.Run("OverrideNil", func(t *testing.T) {
		settings := &AWSSessionSettings{SharedCredentialsFile: []string{"plain-path"}}
		provider := rootCredentialsProvider(settings, factories)
		require.NotNil(t, provider)

		cache, ok := provider.(*aws.CredentialsCache)
		require.True(t, ok)
		assert.True(t, cache.IsCredentialsProvider(&refreshableSharedCredentialsProvider{}))
	})

	t.Run("PreCached", func(t *testing.T) {
		preCachedFactories := []awsv2.CredentialsProviderFactory{
			func(string) aws.CredentialsProvider {
				return aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{AccessKeyID: "pre-cached"}, nil
				}))
			},
		}
		settings := &AWSSessionSettings{SharedCredentialsFile: []string{"any"}}
		provider := rootCredentialsProvider(settings, preCachedFactories)
		require.NotNil(t, provider)

		// ensureCached returns the input directly when it's already a *aws.CredentialsCache.
		cache, ok := provider.(*aws.CredentialsCache)
		require.True(t, ok)
		got, err := cache.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "pre-cached", got.AccessKeyID)
	})
}

func TestBuildCredentialProviderChain_NoFilesNoFactoryInvocation(t *testing.T) {
	factories := []awsv2.CredentialsProviderFactory{
		func(string) aws.CredentialsProvider {
			t.Fatalf("override factory unexpectedly invoked when no files configured")
			return nil
		},
	}
	chain := buildCredentialProviderChain(&AWSSessionSettings{}, factories)
	assert.Empty(t, chain)
}

func TestBuildCredentialProviderChain_FactoryNilFiltered(t *testing.T) {
	factories := []awsv2.CredentialsProviderFactory{
		func(file string) aws.CredentialsProvider {
			if file == "match" {
				return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{}, nil
				})
			}
			return nil
		},
	}

	settings := &AWSSessionSettings{SharedCredentialsFile: []string{"nope", "match", "also-nope"}}
	chain := buildCredentialProviderChain(settings, factories)
	require.Len(t, chain, 4, "expect 1 non-nil factory result (matched \"match\" only) + 3 per-file refreshables")
}
