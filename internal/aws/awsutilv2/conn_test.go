// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
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
		client := &mockHTTPClient{}
		got, err := resolveRegion(t.Context(), zap.NewNop(), &AWSSessionSettings{}, client)
		require.Error(t, err)
		assert.ErrorContains(t, err, "failed to resolve region from EC2 metadata")
		assert.Empty(t, got)
		client.AssertNotCalled(t, "Do")
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

func TestGetAWSConfig_Region(t *testing.T) {
	t.Run("FromEnv", func(t *testing.T) {
		isolateAWSEnv(t)
		t.Setenv("AWS_REGION", "eu-west-2")

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{LocalMode: true})
		require.NoError(t, err)
		assert.Equal(t, "eu-west-2", cfg.Region)
	})

	t.Run("FromSettings", func(t *testing.T) {
		isolateAWSEnv(t)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:    "ap-northeast-1",
			LocalMode: true,
		})
		require.NoError(t, err)
		assert.Equal(t, "ap-northeast-1", cfg.Region)
	})
}

func TestGetAWSConfig_SharedCredentials(t *testing.T) {
	credsPath := tempCredentialsFile(t)

	t.Run("FromEnv", func(t *testing.T) {
		isolateAWSEnv(t)
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)
		t.Setenv("AWS_PROFILE", testProfile)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:    testRegion,
			LocalMode: true,
		})
		require.NoError(t, err)

		creds, err := cfg.Credentials.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "ASIAIKJ", creds.AccessKeyID)
	})

	t.Run("FromSettings", func(t *testing.T) {
		isolateAWSEnv(t)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:                testRegion,
			LocalMode:             true,
			Profile:               testProfile,
			SharedCredentialsFile: []string{credsPath},
		})
		require.NoError(t, err)

		creds, err := cfg.Credentials.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "ASIAIKJ", creds.AccessKeyID)
	})
}

func TestGetAWSConfig_CABundle(t *testing.T) {
	certPath := writeSelfSignedCert(t)

	t.Run("FromEnv", func(t *testing.T) {
		isolateAWSEnv(t)
		t.Setenv("AWS_CA_BUNDLE", certPath)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:    testRegion,
			LocalMode: true,
		})
		require.NoError(t, err)
		assertHTTPClientHasRootCAs(t, cfg.HTTPClient)
	})

	t.Run("FromSettings", func(t *testing.T) {
		isolateAWSEnv(t)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:              testRegion,
			LocalMode:           true,
			CertificateFilePath: certPath,
		})
		require.NoError(t, err)
		assertHTTPClientHasRootCAs(t, cfg.HTTPClient)
	})
}

func TestGetAWSConfig_Proxy(t *testing.T) {
	const proxy = "http://proxy.example.com:3128"

	t.Run("FromEnv", func(t *testing.T) {
		isolateAWSEnv(t)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:    testRegion,
			LocalMode: true,
		})
		require.NoError(t, err)
		bc, ok := cfg.HTTPClient.(*awshttp.BuildableClient)
		require.True(t, ok)
		assert.Equal(t,
			reflect.ValueOf(http.ProxyFromEnvironment).Pointer(),
			reflect.ValueOf(bc.GetTransport().Proxy).Pointer(),
			"expected http.ProxyFromEnvironment when no explicit proxy is set")
	})

	t.Run("FromSettings", func(t *testing.T) {
		isolateAWSEnv(t)

		cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
			Region:       testRegion,
			LocalMode:    true,
			ProxyAddress: proxy,
		})
		require.NoError(t, err)
		assert.Equal(t, proxy, resolvedProxyAddr(t, cfg.HTTPClient))
	})
}

func TestGetAWSConfig_Endpoint(t *testing.T) {
	const endpoint = "https://endpoint.example.com"

	isolateAWSEnv(t)

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:    testRegion,
		LocalMode: true,
		Endpoint:  endpoint,
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.BaseEndpoint)
	assert.Equal(t, endpoint, *cfg.BaseEndpoint)
}

// assertHTTPClientHasRootCAs verifies the HTTP client's TLS config has a custom CA pool loaded.
func assertHTTPClientHasRootCAs(t *testing.T, httpClient aws.HTTPClient) {
	t.Helper()
	bc, ok := httpClient.(*awshttp.BuildableClient)
	require.True(t, ok, "expected *awshttp.BuildableClient")
	require.NotNil(t, bc.GetTransport().TLSClientConfig)
	assert.NotNil(t, bc.GetTransport().TLSClientConfig.RootCAs,
		"CA bundle should be populated in TLS config")
}

// resolvedProxyAddr returns the HTTP client's resolved proxy URL string, or empty if no proxy.
func resolvedProxyAddr(t *testing.T, httpClient aws.HTTPClient) string {
	t.Helper()
	bc, ok := httpClient.(*awshttp.BuildableClient)
	require.True(t, ok, "expected *awshttp.BuildableClient")
	got := resolvedProxy(t, bc.GetTransport())
	if got == nil {
		return ""
	}
	return got.String()
}

// TestGetAWSConfig_StaticCredentialsFromEnv confirms AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY flow into the
// credential chain when no Profile or SharedCredentialsFile override is configured.
func TestGetAWSConfig_StaticCredentialsFromEnv(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:    testRegion,
		LocalMode: true,
	})
	require.NoError(t, err)

	creds, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "AKIAEXAMPLE", creds.AccessKeyID)
	assert.Equal(t, "secret", creds.SecretAccessKey)
}

// TestGetAWSConfig_WebIdentityFromEnv confirms AWS_WEB_IDENTITY_TOKEN_FILE activates the web identity
// provider. The token file is non-existent so Retrieve fails deterministically with a token-read error.
func TestGetAWSConfig_WebIdentityFromEnv(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/nonexistent/web-identity-token")
	t.Setenv("AWS_ROLE_ARN", testRoleARN)

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:    testRegion,
		LocalMode: true,
	})
	require.NoError(t, err)

	_, retrieveErr := cfg.Credentials.Retrieve(t.Context())
	require.Error(t, retrieveErr)
	assert.Contains(t, strings.ToLower(retrieveErr.Error()), "web-identity-token",
		"web identity provider should have attempted to read the configured token file")
}

// TestGetAWSConfig_ContainerCredentialsFromEnv confirms AWS_CONTAINER_CREDENTIALS_FULL_URI activates the
// container provider, using a local httptest server that returns synthetic credentials.
func TestGetAWSConfig_ContainerCredentialsFromEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"AccessKeyId":     "AKIACONTAINER",
			"SecretAccessKey": "container-secret",
			"Token":           "container-token",
			"Expiration":      "2099-01-01T00:00:00Z"
		}`))
	}))
	defer server.Close()

	isolateAWSEnv(t)
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", server.URL+"/credentials")

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:    testRegion,
		LocalMode: true,
	})
	require.NoError(t, err)

	creds, err := cfg.Credentials.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "AKIACONTAINER", creds.AccessKeyID,
		"creds should originate from the container credentials endpoint")
}

// TestGetAWSConfig_DualStackEndpointFromEnv confirms AWS_USE_DUALSTACK_ENDPOINT
// is reflected in cfg.ConfigSources. Env-only — no AWSSessionSettings field.
func TestGetAWSConfig_DualStackEndpointFromEnv(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_USE_DUALSTACK_ENDPOINT", "true")

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:    testRegion,
		LocalMode: true,
	})
	require.NoError(t, err)

	state, found := getDualStackEndpointState(cfg)
	require.True(t, found, "AWS_USE_DUALSTACK_ENDPOINT should be reflected in ConfigSources")
	assert.Equal(t, aws.DualStackEndpointStateEnabled, state)
}

// TestGetAWSConfig_FIPSEndpointFromEnv confirms AWS_USE_FIPS_ENDPOINT is
// reflected in cfg.ConfigSources. Env-only — no AWSSessionSettings field.
func TestGetAWSConfig_FIPSEndpointFromEnv(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_USE_FIPS_ENDPOINT", "true")

	cfg, err := GetAWSConfig(t.Context(), zap.NewNop(), &AWSSessionSettings{
		Region:    testRegion,
		LocalMode: true,
	})
	require.NoError(t, err)

	state, found := getFIPSEndpointState(cfg)
	require.True(t, found, "AWS_USE_FIPS_ENDPOINT should be reflected in ConfigSources")
	assert.Equal(t, aws.FIPSEndpointStateEnabled, state)
}

// getDualStackEndpointState mirrors how AWS service clients resolve the flag at
// request time.
func getDualStackEndpointState(cfg aws.Config) (aws.DualStackEndpointState, bool) {
	for _, src := range cfg.ConfigSources {
		if v, ok := src.(interface {
			GetUseDualStackEndpoint(context.Context) (aws.DualStackEndpointState, bool, error)
		}); ok {
			state, found, _ := v.GetUseDualStackEndpoint(context.Background())
			if found {
				return state, true
			}
		}
	}
	return aws.DualStackEndpointStateUnset, false
}

// getFIPSEndpointState mirrors how AWS service clients resolve the flag at
// request time.
func getFIPSEndpointState(cfg aws.Config) (aws.FIPSEndpointState, bool) {
	for _, src := range cfg.ConfigSources {
		if v, ok := src.(interface {
			GetUseFIPSEndpoint(context.Context) (aws.FIPSEndpointState, bool, error)
		}); ok {
			state, found, _ := v.GetUseFIPSEndpoint(context.Background())
			if found {
				return state, true
			}
		}
	}
	return aws.FIPSEndpointStateUnset, false
}
