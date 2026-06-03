// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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

func TestGetAWSConfig_Refreshable(t *testing.T) {
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_PROFILE", "")
	tmpFilename := tempCredentialsFile(t)

	settings := &AWSSessionSettings{
		Region:                testRegion,
		Profile:               testProfile,
		SharedCredentialsFile: []string{tmpFilename},
	}

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.NoError(t, err)
	assert.Equal(t, testRegion, cfg.Region)
	require.NotNil(t, cfg.Credentials)
	cache, ok := cfg.Credentials.(*aws.CredentialsCache)
	require.True(t, ok)
	assert.True(t, cache.IsCredentialsProvider(&refreshableSharedCredentialsProvider{}))

	got, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "ASIAIKJ", got.AccessKeyID)
	assert.Equal(t, "o1rLD3ykKN09", got.SecretAccessKey)
}

func TestWarnIfUnusedSharedConfigFiles(t *testing.T) {
	// Common across all subtests: SDK should not pick up files from these env vars.
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")

	t.Run("NoFiles", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("AWS_SDK_LOAD_CONFIG", "")

		core, observed := observer.New(zap.DebugLevel)
		warnIfUnusedSharedConfigFiles(zap.New(core))
		assert.Equal(t, 0, observed.Len())
	})

	t.Run("CredentialsFile", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("AWS_SDK_LOAD_CONFIG", "")

		credPath := filepath.Join(home, ".aws", "credentials")
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		require.NoError(t, os.WriteFile(credPath, nil, 0o600))

		core, observed := observer.New(zap.WarnLevel)
		warnIfUnusedSharedConfigFiles(zap.New(core))

		require.Equal(t, 1, observed.Len())
		entry := observed.All()[0]
		assert.Equal(t, "Unused shared config file(s) found", entry.Message)
		files, ok := entry.ContextMap()["files"].([]any)
		require.True(t, ok)
		assert.Contains(t, files, credPath)
	})

	t.Run("BothFiles", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("AWS_SDK_LOAD_CONFIG", "true")

		credPath := filepath.Join(home, ".aws", "credentials")
		cfgPath := filepath.Join(home, ".aws", "config")
		require.NoError(t, os.MkdirAll(filepath.Dir(credPath), 0o700))
		require.NoError(t, os.WriteFile(credPath, nil, 0o600))
		require.NoError(t, os.WriteFile(cfgPath, nil, 0o600))

		core, observed := observer.New(zap.WarnLevel)
		warnIfUnusedSharedConfigFiles(zap.New(core))

		require.Equal(t, 1, observed.Len())
		files, ok := observed.All()[0].ContextMap()["files"].([]any)
		require.True(t, ok)
		assert.ElementsMatch(t, []any{cfgPath, credPath}, files)
	})
}

func TestLoadConfig_RetryOnFailure(t *testing.T) {
	attempts := 0
	fn := func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
		attempts++
		if attempts == 1 {
			return aws.Config{}, errors.New("simulated transient failure")
		}
		return config.LoadDefaultConfig(ctx, optFns...)
	}

	settings := &AWSSessionSettings{Region: testRegion}
	cfg, err := getAWSConfig(t.Context(), zap.NewNop(), settings, 10*time.Millisecond, fn)
	require.NoError(t, err)
	assert.Equal(t, 2, attempts, "should retry once after first failure")
	assert.Equal(t, testRegion, cfg.Region)
}

func TestLoadConfig_RetryDelayCancelledByContext(t *testing.T) {
	fn := func(_ context.Context, _ ...func(*config.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("first attempt fails")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before the retry delay fires

	settings := &AWSSessionSettings{Region: testRegion}
	_, err := getAWSConfig(ctx, zap.NewNop(), settings, time.Hour, fn)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func applyOptions(t *testing.T, opts []func(*config.LoadOptions) error) config.LoadOptions {
	t.Helper()
	var lo config.LoadOptions
	for _, fn := range opts {
		require.NoError(t, fn(&lo))
	}
	return lo
}

func TestBuildLoadOptions_Region(t *testing.T) {
	t.Run("Set", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{}, testRegion, nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Equal(t, testRegion, lo.Region)
	})
	t.Run("Empty", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Empty(t, lo.Region)
	})
}

func TestBuildLoadOptions_Endpoint(t *testing.T) {
	t.Run("Set", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{Endpoint: "https://endpoint.example.com"}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Equal(t, "https://endpoint.example.com", lo.BaseEndpoint)
	})
	t.Run("Empty", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Empty(t, lo.BaseEndpoint)
	})
}

func TestBuildLoadOptions_MaxRetries(t *testing.T) {
	t.Run("Positive", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{MaxRetries: 5}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Equal(t, 6, lo.RetryMaxAttempts)
	})
	t.Run("Zero", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Equal(t, 1, lo.RetryMaxAttempts)
	})
	t.Run("Negative", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{MaxRetries: -3}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Equal(t, 1, lo.RetryMaxAttempts)
	})
}

func TestBuildLoadOptions_Provider(t *testing.T) {
	t.Run("Set", func(t *testing.T) {
		p := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test"}, nil
		})
		opts := buildLoadOptions(&AWSSessionSettings{}, "", nil, nil, p)
		lo := applyOptions(t, opts)
		require.NotNil(t, lo.Credentials)
	})
	t.Run("Nil", func(t *testing.T) {
		opts := buildLoadOptions(&AWSSessionSettings{}, "", nil, nil, nil)
		lo := applyOptions(t, opts)
		assert.Nil(t, lo.Credentials)
	})
}
