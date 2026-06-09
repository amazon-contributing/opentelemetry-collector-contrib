// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProfile = "default"

func TestSharedCredentialsProvider_MissingFile(t *testing.T) {
	// With an explicit credentials file the SDK reads only that file (ConfigFiles
	// is emptied), so a missing file fails loudly even for the default profile
	// rather than silently resolving empty credentials from elsewhere.
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envAwsSharedCredentialsFile, "")
	t.Setenv(envAwsSharedConfigFile, "")
	tmp := filepath.Join(t.TempDir(), "missing")
	p := SharedCredentialsProvider{Filename: tmp, Profile: testProfile}
	_, err := p.Retrieve(context.Background())
	require.Error(t, err)
}

func TestRefreshableSharedCredentialsProvider_DefaultsExpiryWindow(t *testing.T) {
	tmpFile := writeTempCredentials(t, "credential_original")
	p := RefreshableSharedCredentialsProvider{
		Provider: SharedCredentialsProvider{Filename: tmpFile, Profile: testProfile},
		// ExpiryWindow zero → defaultExpiryWindow.
	}
	got, err := p.Retrieve(context.Background())
	require.NoError(t, err)
	assert.True(t, got.CanExpire)
	expectedMin := time.Now().Add(defaultExpiryWindow - time.Minute)
	expectedMax := time.Now().Add(defaultExpiryWindow + time.Minute)
	assert.WithinRange(t, got.Expires, expectedMin, expectedMax)
}

func TestRefreshableSharedCredentialsProvider_FileRotation(t *testing.T) {
	// Write fixture 1, retrieve, rotate file, wait past expiry, retrieve
	// again, verify the rotated value comes through.
	tmpDir := t.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "credential")
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	provider := RefreshableSharedCredentialsProvider{
		Provider:     SharedCredentialsProvider{Filename: tmpFile.Name(), Profile: testProfile},
		ExpiryWindow: 500 * time.Millisecond,
	}
	cache := aws.NewCredentialsCache(provider)

	originalContent, err := os.ReadFile(filepath.Join("testdata", "credential_original"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpFile.Name(), originalContent, 0o600))

	creds, err := cache.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "o1rLD3ykKN09originalSECRETxxxxxxxxxxxxxxxx", creds.SecretAccessKey)
	assert.False(t, creds.Expired())

	time.Sleep(100 * time.Millisecond)
	assert.False(t, creds.Expired())

	rotatedContent, err := os.ReadFile(filepath.Join("testdata", "credential_rotate"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpFile.Name(), rotatedContent, 0o600))

	time.Sleep(500 * time.Millisecond)
	assert.True(t, creds.Expired())

	creds, err = cache.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "o1rLDaaacccROTATEDsecretxxxxxxxxxxxxxxxxxx", creds.SecretAccessKey)
	assert.False(t, creds.Expired())
}

// writeTempCredentials copies a fixture into a temp file and returns its path.
func writeTempCredentials(t *testing.T, fixtureName string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", fixtureName))
	require.NoError(t, err)
	tmp := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(tmp, content, 0o600))
	return tmp
}

func TestSharedCredentialsProvider_EmptyProfileDefaultsToDefault(t *testing.T) {
	// An empty Profile must resolve to "default" rather than be passed through
	// to LoadSharedConfigProfile, which rejects an empty profile name. HOME is
	// pointed at a temp dir so real ~/.aws files cannot influence the result.
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envAwsProfile, "")
	t.Setenv(envAwsSharedCredentialsFile, "")
	t.Setenv(envAwsSharedConfigFile, "")
	tmpFile := writeTempCredentials(t, "credential_original")

	p := SharedCredentialsProvider{Filename: tmpFile, Profile: ""}
	creds, err := p.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "o1rLD3ykKN09originalSECRETxxxxxxxxxxxxxxxx", creds.SecretAccessKey)
}

func TestSharedCredentialsProvider_EmptyProfileHonorsAwsProfileEnv(t *testing.T) {
	// An empty Profile falls back to AWS_PROFILE before "default".
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envAwsSharedCredentialsFile, "")
	t.Setenv(envAwsSharedConfigFile, "")
	tmp := filepath.Join(t.TempDir(), "credentials")
	require.NoError(t, os.WriteFile(tmp, []byte("[custom]\naws_access_key_id = AKIDEXAMPLE\naws_secret_access_key = customSecretValue\n"), 0o600))
	t.Setenv(envAwsProfile, "custom")

	p := SharedCredentialsProvider{Filename: tmp, Profile: ""}
	creds, err := p.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "customSecretValue", creds.SecretAccessKey)
}
