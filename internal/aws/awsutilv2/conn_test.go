// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestResolveRegion(t *testing.T) {
	t.Run("FromSettings", func(t *testing.T) {
		t.Setenv("AWS_REGION", "env-region")
		got, err := resolveRegion(t.Context(), zap.NewNop(), &AWSSessionSettings{Region: testRegion}, nil)
		require.NoError(t, err)
		assert.Equal(t, testRegion, got)
	})
	t.Run("FromEnv", func(t *testing.T) {
		t.Setenv("AWS_REGION", "env-region")
		got, err := resolveRegion(t.Context(), zap.NewNop(), &AWSSessionSettings{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "env-region", got)
	})
	t.Run("LocalModeError", func(t *testing.T) {
		t.Setenv("AWS_REGION", "")
		got, err := resolveRegion(t.Context(), zap.NewNop(), &AWSSessionSettings{LocalMode: true}, nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "local_mode")
		assert.Empty(t, got)
	})
	t.Run("IMDSDisabledError", func(t *testing.T) {
		t.Setenv("AWS_REGION", "")
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		client, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{})
		require.NoError(t, err)
		got, err := resolveRegion(t.Context(), zap.NewNop(), &AWSSessionSettings{}, client)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to resolve region from EC2 metadata")
		assert.Empty(t, got)
	})
}

func TestResolveRegionFromIMDS(t *testing.T) {
	t.Run("StrictV2Succeeds", func(t *testing.T) {
		var seen []imds.Options
		stubGetRegionFromIMDS(t, func(_ context.Context, opts imds.Options) (string, error) {
			seen = append(seen, opts)
			return testRegion, nil
		})

		got, err := resolveRegionFromIMDS(t.Context(), zap.NewNop(), 0, nil)
		require.NoError(t, err)
		assert.Equal(t, testRegion, got)
		require.Len(t, seen, 1, "permissive client should not be invoked when strict succeeds")
		assert.Equal(t, aws.FalseTernary, seen[0].EnableFallback, "strict client must disable fallback")
	})

	t.Run("StrictFailsPermissiveSucceeds", func(t *testing.T) {
		var seen []imds.Options
		stubGetRegionFromIMDS(t, func(_ context.Context, opts imds.Options) (string, error) {
			seen = append(seen, opts)
			if len(seen) == 1 {
				return "", errors.New("strict v2 failed")
			}
			return testRegion, nil
		})

		got, err := resolveRegionFromIMDS(t.Context(), zap.NewNop(), 0, nil)
		require.NoError(t, err)
		assert.Equal(t, testRegion, got)
		require.Len(t, seen, 2, "should attempt strict then permissive")
		assert.Equal(t, aws.FalseTernary, seen[0].EnableFallback, "first call must be strict")
		assert.Equal(t, aws.TrueTernary, seen[1].EnableFallback, "second call must enable fallback")
	})

	t.Run("BothFail", func(t *testing.T) {
		var seen []imds.Options
		stubGetRegionFromIMDS(t, func(_ context.Context, opts imds.Options) (string, error) {
			seen = append(seen, opts)
			return "", errors.New("imds unavailable")
		})

		_, err := resolveRegionFromIMDS(t.Context(), zap.NewNop(), 0, nil)
		require.Error(t, err)
		assert.Len(t, seen, 2)
	})
}

func stubGetRegionFromIMDS(t *testing.T, stub func(context.Context, imds.Options) (string, error)) {
	t.Helper()
	getRegionFromIMDS = stub
	t.Cleanup(func() { getRegionFromIMDS = getIMDSRegion })
}

func TestGetAWSConfig_AssumeRole(t *testing.T) {
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("HOME", t.TempDir())

	expiry := time.Now().Add(time.Hour)
	stubCreds := types.Credentials{
		AccessKeyId:     aws.String("STS-AK"),
		SecretAccessKey: aws.String("STS-SK"),
		SessionToken:    aws.String("STS-TOKEN"),
		Expiration:      &expiry,
	}
	orig := newAssumeRoleClient
	t.Cleanup(func() { newAssumeRoleClient = orig })
	newAssumeRoleClient = func(_ aws.Config) stscreds.AssumeRoleAPIClient {
		return stubAssumeRoleAPIClient{out: &sts.AssumeRoleOutput{Credentials: &stubCreds}}
	}

	settings := &AWSSessionSettings{
		Region:  testRegion,
		RoleARN: testRoleARN,
	}

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.NoError(t, err)
	assert.Equal(t, testRegion, cfg.Region)

	got, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "STS-AK", got.AccessKeyID)
	assert.Equal(t, "STS-SK", got.SecretAccessKey)
	assert.Equal(t, "STS-TOKEN", got.SessionToken)
}

type stubAssumeRoleAPIClient struct {
	out *sts.AssumeRoleOutput
}

func (s stubAssumeRoleAPIClient) AssumeRole(_ context.Context, _ *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return s.out, nil
}

func TestGetAWSConfig_DoesNotMutateSettings(t *testing.T) {
	// GetAWSConfig must not write back to the caller's *AWSSessionSettings.
	tmpFilename := tempCredentialsFile(t)

	settings := &AWSSessionSettings{
		Region:                testRegion,
		Profile:               testProfile,
		Endpoint:              "https://endpoint.example.com",
		SharedCredentialsFile: []string{tmpFilename},
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
		MaxRetries:            2,
		IMDSRetries:           3,
	}
	before := *settings

	_, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.NoError(t, err)

	assert.Equal(t, before, *settings)
}

func TestGetAWSConfig_HTTPClientError(t *testing.T) {
	settings := &AWSSessionSettings{Region: testRegion, ProxyAddress: "://invalid"}
	_, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.Error(t, err)
}

func TestGetAWSConfig_RegionResolutionError(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	settings := &AWSSessionSettings{LocalMode: true}
	_, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.Error(t, err)
	assert.ErrorContains(t, err, "local_mode")
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

func TestGetAWSConfig_RetryOnFailure(t *testing.T) {
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

func TestGetAWSConfig_RetryDelayCancelledByContext(t *testing.T) {
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
		opts := testBuildLoadOptions(&AWSSessionSettings{}, testRegion)
		lo := applyOptions(t, opts)
		assert.Equal(t, testRegion, lo.Region)
	})
	t.Run("Empty", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{}, "")
		lo := applyOptions(t, opts)
		assert.Empty(t, lo.Region)
	})
}

func TestBuildLoadOptions_Endpoint(t *testing.T) {
	t.Run("Set", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{Endpoint: "https://endpoint.example.com"}, "")
		lo := applyOptions(t, opts)
		assert.Equal(t, "https://endpoint.example.com", lo.BaseEndpoint)
	})
	t.Run("Empty", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{}, "")
		lo := applyOptions(t, opts)
		assert.Empty(t, lo.BaseEndpoint)
	})
}

func TestBuildLoadOptions_MaxRetries(t *testing.T) {
	t.Run("Positive", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{MaxRetries: 5}, "")
		lo := applyOptions(t, opts)
		assert.Equal(t, 6, lo.RetryMaxAttempts)
	})
	t.Run("Zero", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{}, "")
		lo := applyOptions(t, opts)
		assert.Equal(t, 1, lo.RetryMaxAttempts)
	})
	t.Run("Negative", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{MaxRetries: -3}, "")
		lo := applyOptions(t, opts)
		assert.Equal(t, 1, lo.RetryMaxAttempts)
	})
}

func TestBuildLoadOptions_Provider(t *testing.T) {
	t.Run("Set", func(t *testing.T) {
		p := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "test"}, nil
		})
		opts := buildLoadOptions(&AWSSessionSettings{}, "", nil, nil, nil, p)
		lo := applyOptions(t, opts)
		require.NotNil(t, lo.Credentials)
	})
	t.Run("Nil", func(t *testing.T) {
		opts := testBuildLoadOptions(&AWSSessionSettings{}, "")
		lo := applyOptions(t, opts)
		assert.Nil(t, lo.Credentials)
	})
}

// testBuildLoadOptions is a test helper for the common case of calling buildLoadOptions
// with only settings and region; shared config files, HTTP client, and credentials provider are nil.
func testBuildLoadOptions(settings *AWSSessionSettings, region string) []func(*config.LoadOptions) error {
	return buildLoadOptions(settings, region, nil, nil, nil, nil)
}
