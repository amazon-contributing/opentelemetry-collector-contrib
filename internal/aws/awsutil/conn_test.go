// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestResolveRegion_PriorityOrder(t *testing.T) {
	logger := zap.NewNop()

	t.Run("ConfigRegionWinsOverEnv", func(t *testing.T) {
		t.Setenv("AWS_REGION", "env-region")
		s := &AWSSessionSettings{Region: "config-region"}
		got := resolveRegion(t.Context(), logger, s)
		assert.Equal(t, "config-region", got)
	})

	t.Run("EnvWinsWhenConfigEmpty", func(t *testing.T) {
		t.Setenv("AWS_REGION", "env-region")
		s := &AWSSessionSettings{}
		got := resolveRegion(t.Context(), logger, s)
		assert.Equal(t, "env-region", got)
	})

	t.Run("LocalModeSkipsIMDS", func(t *testing.T) {
		t.Setenv("AWS_REGION", "")
		s := &AWSSessionSettings{LocalMode: true}
		got := resolveRegion(t.Context(), logger, s)
		assert.Empty(t, got)
	})
}

func TestGetAWSConfig_NoRegionResolvable(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		LocalMode:             true,
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
	})
	assert.Error(t, err)
	assert.EqualError(t, err, "region is required when local_mode is enabled")
	assert.Equal(t, aws.Config{}, cfg)
}

// staticCredsEnv installs static AWS credentials via env vars and scrubs
// any inherited shared-config / profile env so config.LoadDefaultConfig
// resolves deterministically without consulting IMDS or the EC2 instance
// role.
func staticCredsEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "")
	t.Setenv("AWS_CONFIG_FILE", "")
	t.Setenv("AWS_SDK_LOAD_CONFIG", "")
}

func TestGetAWSConfig_ExplicitRegion(t *testing.T) {
	staticCredsEnv(t)

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:                "us-east-1",
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
		MaxRetries:            2,
	})
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", cfg.Region)
	// MaxRetries: 2 → RetryMaxAttempts: 3 (v2 counts the initial attempt).
	assert.Equal(t, 3, cfg.RetryMaxAttempts)
}

func TestGetAWSConfig_EndpointThreaded(t *testing.T) {
	staticCredsEnv(t)

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:                "us-east-1",
		Endpoint:              "https://example-endpoint.local",
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.BaseEndpoint)
	assert.Equal(t, "https://example-endpoint.local", *cfg.BaseEndpoint)
}

func TestGetAWSConfig_RetryMaxAttempts(t *testing.T) {
	// settings.MaxRetries=N must produce cfg.RetryMaxAttempts=N+1.
	staticCredsEnv(t)

	tests := []struct {
		maxRetries          int
		wantRetryMaxAttempt int
	}{
		{-2, 1},
		{-1, 1},
		{0, 1},
		{1, 2},
		{2, 3},
		{5, 6},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("MaxRetries=%d", tc.maxRetries), func(t *testing.T) {
			cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
				Region:                "us-east-1",
				NumberOfWorkers:       8,
				RequestTimeoutSeconds: 30,
				MaxRetries:            tc.maxRetries,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantRetryMaxAttempt, cfg.RetryMaxAttempts)
		})
	}
}

func TestGetAWSConfig_DoesNotMutateSettings(t *testing.T) {
	// GetAWSConfig must not write back to the caller's *AWSSessionSettings.
	staticCredsEnv(t)

	settings := &AWSSessionSettings{
		Region:                "us-east-1",
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
		MaxRetries:            2,
		Profile:               "test-profile",
		SharedCredentialsFile: []string{"/tmp/test-credentials"},
		CertificateFilePath:   "testdata/public_amazon_cert.pem",
		IMDSRetries:           3,
		LocalMode:             false,
		ResourceARN:           "arn:aws:resource",
	}
	before := *settings

	_, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.NoError(t, err)

	assert.Equal(t, before, *settings)
}

// captureSTSClientConfigs swaps the STS client constructor seams to record
// every aws.Config they receive, delegating to the real constructors.
func captureSTSClientConfigs(t *testing.T) *[]aws.Config {
	t.Helper()
	var captured []aws.Config
	origAssume := newAssumeRoleClient
	origWebID := newWebIdentityClient
	newAssumeRoleClient = func(cfg aws.Config) stscreds.AssumeRoleAPIClient {
		captured = append(captured, cfg)
		return origAssume(cfg)
	}
	newWebIdentityClient = func(cfg aws.Config) stscreds.AssumeRoleWithWebIdentityAPIClient {
		captured = append(captured, cfg)
		return origWebID(cfg)
	}
	t.Cleanup(func() {
		newAssumeRoleClient = origAssume
		newWebIdentityClient = origWebID
	})
	return &captured
}

// The STS clients used for AssumeRole / AssumeRoleWithWebIdentity must not
// inherit the data-plane BaseEndpoint or retry budget, while the returned
// config must still carry both.
func TestGetAWSConfig_STSClientsIsolatedFromEndpointAndRetries(t *testing.T) {
	staticCredsEnv(t)

	newSettings := func() *AWSSessionSettings {
		return &AWSSessionSettings{
			Region:                "us-east-1",
			Endpoint:              "https://example-endpoint.local",
			RoleARN:               testRoleARN,
			MaxRetries:            2,
			NumberOfWorkers:       8,
			RequestTimeoutSeconds: 30,
		}
	}

	verify := func(t *testing.T, cfg aws.Config, captured []aws.Config) {
		t.Helper()
		require.NotEmpty(t, captured)
		for _, c := range captured {
			assert.Nil(t, c.BaseEndpoint)
			assert.Equal(t, 0, c.RetryMaxAttempts)
		}
		require.NotNil(t, cfg.BaseEndpoint)
		assert.Equal(t, "https://example-endpoint.local", *cfg.BaseEndpoint)
		assert.Equal(t, 3, cfg.RetryMaxAttempts)
	}

	t.Run("AssumeRole", func(t *testing.T) {
		captured := captureSTSClientConfigs(t)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), newSettings())
		require.NoError(t, err)
		verify(t, cfg, *captured)
	})

	t.Run("WebIdentity", func(t *testing.T) {
		captured := captureSTSClientConfigs(t)

		settings := newSettings()
		settings.WebIdentityTokenFile = filepath.Join("testdata", "token_file")
		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
		require.NoError(t, err)
		verify(t, cfg, *captured)
	})
}

// The custom HTTP client (proxy/TLS/timeout settings) must be scoped to the
// data plane: the returned config carries it, while the configs handed to
// the STS client constructors must not.
func TestGetAWSConfig_CustomHTTPClientScopedToDataPlane(t *testing.T) {
	staticCredsEnv(t)

	settings := &AWSSessionSettings{
		Region:                "us-east-1",
		RoleARN:               testRoleARN,
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
	}
	// getHTTPClient caches by settings, so this returns the same instance
	// GetAWSConfig attaches to the returned config.
	customClient, err := getHTTPClient(zap.NewNop(), settings)
	require.NoError(t, err)

	captured := captureSTSClientConfigs(t)

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), settings)
	require.NoError(t, err)

	assert.Same(t, customClient, cfg.HTTPClient, "returned config must carry the custom client")
	require.NotEmpty(t, *captured)
	for _, c := range *captured {
		assert.Nil(t, c.HTTPClient, "STS clients must not carry the custom client (SDK default expected)")
	}
}

type fakeIMDSRegionClient struct {
	region string
}

func (f fakeIMDSRegionClient) GetRegion(context.Context, *imds.GetRegionInput, ...func(*imds.Options)) (*imds.GetRegionOutput, error) {
	return &imds.GetRegionOutput{Region: f.region}, nil
}

// The IMDS region-lookup client must be built without the custom HTTP
// client (SDK default IMDS client behavior).
func TestResolveRegionFromIMDS_NoCustomHTTPClient(t *testing.T) {
	orig := newIMDSClient
	t.Cleanup(func() { newIMDSClient = orig })

	var gotOpts imds.Options
	newIMDSClient = func(_ *zap.Logger, _ int, optFns ...func(*imds.Options)) imdsRegionClient {
		for _, fn := range optFns {
			fn(&gotOpts)
		}
		return fakeIMDSRegionClient{region: "eu-west-1"}
	}

	region, err := resolveRegionFromIMDS(t.Context(), zap.NewNop(), 1)
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", region)
	assert.Nil(t, gotOpts.HTTPClient, "IMDS region lookup must not carry the custom client")
}
