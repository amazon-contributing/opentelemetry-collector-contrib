// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubWebIdentityClient is a mock stscreds.AssumeRoleWithWebIdentityAPIClient.
type stubWebIdentityClient struct {
	creds *ststypes.Credentials
	err   error
}

func (s stubWebIdentityClient) AssumeRoleWithWebIdentity(
	_ context.Context,
	_ *sts.AssumeRoleWithWebIdentityInput,
	_ ...func(*sts.Options),
) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &sts.AssumeRoleWithWebIdentityOutput{Credentials: s.creds}, nil
}

// withStubWebIdentityClient swaps newWebIdentityClient for the duration of a test.
func withStubWebIdentityClient(t *testing.T, stub stubWebIdentityClient) {
	t.Helper()
	orig := newWebIdentityClient
	newWebIdentityClient = func(aws.Config) stscreds.AssumeRoleWithWebIdentityAPIClient { return stub }
	t.Cleanup(func() { newWebIdentityClient = orig })
}

func mockWebIdentityCreds() *ststypes.Credentials {
	return &ststypes.Credentials{
		AccessKeyId:     aws.String("AKIAWEBIDENTITY"),
		SecretAccessKey: aws.String("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		SessionToken:    aws.String("SessionToken"),
		Expiration:      aws.Time(time.Now().Add(time.Hour)),
	}
}

func TestGetAWSConfig_WebIdentityFromSettings(t *testing.T) {
	withStubWebIdentityClient(t, stubWebIdentityClient{creds: mockWebIdentityCreds()})

	settings := CreateDefaultSessionConfig()
	settings.Region = testRegion
	settings.RoleARN = testRoleARN
	settings.WebIdentityTokenFile = filepath.Join("testdata", "token_file")

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &settings)
	require.NoError(t, err)

	creds, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "AKIAWEBIDENTITY", creds.AccessKeyID)
}

func TestGetAWSConfig_WebIdentity_MissingFileFailsLazily(t *testing.T) {
	// No STS client stub is needed: Retrieve fails earlier, in the token-file read
	// (stscreds.IdentityTokenFile.GetIdentityToken), before the STS client is called.
	settings := CreateDefaultSessionConfig()
	settings.Region = testRegion
	settings.RoleARN = testRoleARN
	settings.WebIdentityTokenFile = filepath.Join("testdata", "does_not_exist")

	// Config resolution succeeds even though the token file is absent: the
	// token is read lazily on first Retrieve (mirrors a projected SA token
	// that is not yet present at startup).
	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &settings)
	require.NoError(t, err)

	_, err = cfg.Credentials.Retrieve(t.Context())
	require.Error(t, err)
}

func TestGetAWSConfig_WebIdentity_MissingRoleARN(t *testing.T) {
	settings := CreateDefaultSessionConfig()
	settings.Region = testRegion
	settings.WebIdentityTokenFile = filepath.Join("testdata", "token_file")

	_, err := GetAWSConfig(t.Context(), zap.NewNop(), &settings)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "role_arn")
}

func TestNewWebIdentityCredentialsProvider_KnownPartition(t *testing.T) {
	tr := stscreds.IdentityTokenFile(filepath.Join("testdata", "token_file"))
	provider := newWebIdentityCredentialsProvider(testAWSConfig, testRoleARN, testRegion, tr)

	stsProvider, ok := provider.(*stsCredentialsProvider)
	require.True(t, ok)
	require.NotNil(t, stsProvider.regional)
	assert.NotNil(t, stsProvider.partitional)

	_, ok = stsProvider.regional.(*stscreds.WebIdentityRoleProvider)
	assert.True(t, ok)
}
