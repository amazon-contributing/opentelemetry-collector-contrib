// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
