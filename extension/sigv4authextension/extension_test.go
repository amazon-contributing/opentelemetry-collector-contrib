// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

func TestNewSigv4Extension(t *testing.T) {
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "region"},
		Service:            "service",
		AssumeRole:         AssumeRole{ARN: "rolearn", STSRegion: "region"},
	}

	sa := newSigv4Extension(cfg, nil, "awsSDKInfo", zap.NewNop())
	assert.Equal(t, cfg.Region, sa.cfg.Region)
	assert.Equal(t, cfg.Service, sa.cfg.Service)
	assert.Equal(t, cfg.AssumeRole.ARN, sa.cfg.AssumeRole.ARN)
}

func TestRoundTripper(t *testing.T) {
	awsCredsProvider := mockCredentials()

	base := (http.RoundTripper)(http.DefaultTransport.(*http.Transport).Clone())
	awsSDKInfo := "awsSDKInfo"
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "region"},
		Service:            "service",
		AssumeRole:         AssumeRole{ARN: "rolearn", STSRegion: "region"},
	}

	sa := newSigv4Extension(cfg, awsCredsProvider, awsSDKInfo, zap.NewNop())
	assert.NotNil(t, sa)

	rt, err := sa.RoundTripper(base)
	assert.NoError(t, err)

	si := rt.(*signingRoundTripper)
	assert.Equal(t, base, si.transport)
	assert.Equal(t, cfg.Region, si.region)
	assert.Equal(t, cfg.Service, si.service)
	assert.Equal(t, awsSDKInfo, si.awsSDKInfo)
	assert.Equal(t, awsCredsProvider, si.credsProvider)
}

func TestGetCredsProviderFromConfig(t *testing.T) {
	tests := []struct {
		name            string
		cfg             *Config
		AccessKeyID     string
		SecretAccessKey string
		shouldError     bool
	}{
		{
			"success_case_without_role",
			&Config{
				AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "region"},
				Service:            "service",
				AssumeRole:         AssumeRole{STSRegion: "region"},
			},
			"AccessKeyID",
			"SecretAccessKey",
			false,
		},
		{
			"failure_case_without_role",
			&Config{
				AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "region"},
				Service:            "service",
				AssumeRole:         AssumeRole{STSRegion: "region"},
			},
			"",
			"",
			true,
		},
	}
	// run tests
	for _, testcase := range tests {
		t.Run(testcase.name, func(t *testing.T) {
			isolateAWSEnv(t)
			t.Setenv("AWS_ACCESS_KEY_ID", testcase.AccessKeyID)
			t.Setenv("AWS_SECRET_ACCESS_KEY", testcase.SecretAccessKey)
			credsProvider, err := getCredsProviderFromConfig(t.Context(), zap.NewNop(), testcase.cfg)

			if testcase.shouldError {
				assert.Error(t, err)
				assert.Nil(t, credsProvider)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, credsProvider)

			creds, err := (*credsProvider).Retrieve(t.Context())
			require.NoError(t, err)
			require.NotNil(t, creds)
		})
	}
}

func TestGetCredsProviderFromConfig_SharedCredentialsFile(t *testing.T) {
	isolateAWSEnv(t)
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{
			Region:                "region",
			SharedCredentialsFile: []string{filepath.Join("testdata", "credentials")},
		},
		Service:    "service",
		AssumeRole: AssumeRole{STSRegion: "region"},
	}

	credsProvider, err := getCredsProviderFromConfig(t.Context(), zap.NewNop(), cfg)
	require.NoError(t, err)
	require.NotNil(t, credsProvider)

	creds, err := (*credsProvider).Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "FAKEAWSACCESSKEYID00", creds.AccessKeyID)
}

func TestGetCredsProviderFromConfig_Profile(t *testing.T) {
	isolateAWSEnv(t)
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{
			Region:                "region",
			Profile:               "testprofile",
			SharedCredentialsFile: []string{filepath.Join("testdata", "credentials")},
		},
		Service:    "service",
		AssumeRole: AssumeRole{STSRegion: "region"},
	}

	credsProvider, err := getCredsProviderFromConfig(t.Context(), zap.NewNop(), cfg)
	require.NoError(t, err)
	require.NotNil(t, credsProvider)

	creds, err := (*credsProvider).Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "FAKEAWSACCESSKEYID01", creds.AccessKeyID)
}

func TestGetCredsProviderFromConfig_LocalMode(t *testing.T) {
	isolateAWSEnv(t)
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{
			Region:                "region",
			LocalMode:             true,
			SharedCredentialsFile: []string{filepath.Join("testdata", "credentials")},
		},
		Service:    "service",
		AssumeRole: AssumeRole{STSRegion: "region"},
	}

	credsProvider, err := getCredsProviderFromConfig(t.Context(), zap.NewNop(), cfg)
	require.NoError(t, err)
	require.NotNil(t, credsProvider)
}

func TestGetCredsProviderFromWebIdentityConfig(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *Config
		shouldError bool
	}{
		{
			"valid_token_with_assume_role_arn",
			&Config{
				AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "region"},
				Service:            "service",
				AssumeRole:         AssumeRole{ARN: "arn:aws:iam::123456789012:role/my_role", WebIdentityTokenFile: "testdata/token_file"},
			},
			false,
		},
		{
			"valid_token_with_role_arn",
			&Config{
				AWSSessionSettings: awsutilv2.AWSSessionSettings{
					Region:  "region",
					RoleARN: "arn:aws:iam::123456789012:role/my_role",
				},
				Service:    "service",
				AssumeRole: AssumeRole{WebIdentityTokenFile: "testdata/token_file"},
			},
			false,
		},
		{
			"missing_token_file",
			&Config{
				AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "region"},
				Service:            "service",
				AssumeRole:         AssumeRole{ARN: "arn:aws:iam::123456789012:role/my_role", WebIdentityTokenFile: "testdata/no_token_file"},
			},
			true,
		},
	}
	// run tests
	for _, testcase := range tests {
		t.Run(testcase.name, func(t *testing.T) {
			isolateAWSEnv(t)
			credsProvider, err := getCredsProviderFromWebIdentityConfig(t.Context(), zap.NewNop(), testcase.cfg)

			if testcase.shouldError {
				assert.Error(t, err)
				assert.Nil(t, credsProvider)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, credsProvider)

			// Should always error out as we are not providing a real token.
			_, err = (*credsProvider).Retrieve(t.Context())
			assert.Error(t, err)
		})
	}
}

func TestCloneRequest(t *testing.T) {
	req1, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	assert.NoError(t, err)

	req2, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	assert.NoError(t, err)
	req2.Header.Add("Header1", "val1")

	tests := []struct {
		name    string
		request *http.Request
	}{
		{
			"no_headers",
			req1,
		},
		{
			"headers",
			req2,
		},
	}
	// run tests
	for _, testcase := range tests {
		t.Run(testcase.name, func(t *testing.T) {
			r2 := cloneRequest(testcase.request)
			assert.Equal(t, testcase.request.Header, r2.Header)
			assert.Equal(t, testcase.request.Body, r2.Body)
		})
	}
}

func mockCredentials() *aws.CredentialsProvider {
	awscfg, _ := awsconfig.LoadDefaultConfig(context.Background())
	provider := credentials.NewStaticCredentialsProvider(
		"MOCK_AWS_ACCESS_KEY",
		"MOCK_AWS_SECRET_ACCESS_KEY",
		"MOCK_TOKEN",
	)

	awscfg.Credentials = aws.NewCredentialsCache(provider)

	return &awscfg.Credentials
}

// isolateAWSEnv prevents the v2 SDK from reading the host's shared credentials
// or config files by overriding HOME, AWS_SHARED_CREDENTIALS_FILE,
// AWS_CONFIG_FILE, AWS_PROFILE, AWS_EC2_METADATA_DISABLED, and the static
// credential env vars. Tests that need specific values for credential env vars
// should call t.Setenv after this helper.
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
}
