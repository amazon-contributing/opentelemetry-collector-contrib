// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testAccountID   = "012345678912"
	testInstanceARN = "arn:aws:ec2:us-east-1:012345678912:instance/i-0123a456700123456"
	testProfile     = "default"
	testRegion      = "us-east-1"
	testRoleARN     = "arn:aws:iam::012345678912:role/XXXXXXXX"
)

// Example credentials pulled from the AWS STS docs:
// https://docs.aws.amazon.com/STS/latest/APIReference/API_GetAccessKeyInfo.html
var (
	testCredentials = aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "SessionToken",
	}
	testAWSConfig = aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(testCredentials.AccessKeyID, testCredentials.SecretAccessKey, testCredentials.SessionToken),
	}
)

type mockHTTPClient struct {
	mock.Mock
}

var _ aws.HTTPClient = (*mockHTTPClient)(nil)

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	return args.Get(0).(*http.Response), args.Error(1)
}

type mockCredentialsProvider struct {
	mock.Mock
}

var _ aws.CredentialsProvider = (*mockCredentialsProvider)(nil)

func (m *mockCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	args := m.Called(ctx)
	return args.Get(0).(aws.Credentials), args.Error(1)
}

// tempCredentialsFile copies the credential_original testdata fixture into t.TempDir and returns the path.
func tempCredentialsFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential")
	content, err := os.ReadFile(filepath.Join("testdata", "credential_original"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

// isolateAWSEnv clears AWS-related env vars and points the SDK at fake config paths so GetAWSConfig
// tests don't pick up state from the host.
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CA_BUNDLE", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("AWS_ENDPOINT_URL", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	t.Setenv("AWS_ROLE_ARN", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	t.Setenv("AWS_USE_DUALSTACK_ENDPOINT", "")
	t.Setenv("AWS_USE_FIPS_ENDPOINT", "")
}
