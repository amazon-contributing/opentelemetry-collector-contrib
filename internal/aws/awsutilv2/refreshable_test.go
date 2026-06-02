// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshableSharedCredentialsProvider(t *testing.T) {
	tmpFilename := tempCredentialsFile(t)

	provider := NewRefreshableSharedCredentialsProvider(tmpFilename, testProfile, 500*time.Millisecond)

	got, err := provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "o1rLD3ykKN09", got.SecretAccessKey)
	assert.False(t, got.Expired())

	// Wait briefly. Credentials should not yet be expired.
	time.Sleep(100 * time.Millisecond)
	assert.False(t, got.Expired())

	// Rotate the file contents.
	rotated, err := os.ReadFile(filepath.Join("testdata", "credential_rotate"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpFilename, rotated, 0o600))

	// Wait for the previous credentials to expire.
	time.Sleep(500 * time.Millisecond)
	assert.True(t, got.Expired())

	got, err = provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "o1rLDaaaccc", got.SecretAccessKey)
	assert.False(t, got.Expired())
}

func TestSharedCredentialsProvider_MissingProfile(t *testing.T) {
	tmpFilename := tempCredentialsFile(t)
	p := NewSharedCredentialsProvider(tmpFilename, "no-such-profile")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}

func TestSharedCredentialsProvider_MissingFileDefaultProfile(t *testing.T) {
	p := NewSharedCredentialsProvider("/nonexistent", "default")
	creds, err := p.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Empty(t, creds.AccessKeyID)
}

func TestSharedCredentialsProvider_MissingFileNonDefaultProfile(t *testing.T) {
	p := NewSharedCredentialsProvider("/nonexistent", "named-profile")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}

func TestSharedCredentialsProvider_EmptyProfileDefaultsToDefault(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	tmpFilename := tempCredentialsFile(t)
	p := NewSharedCredentialsProvider(tmpFilename, "")
	creds, err := p.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "ASIAIKJ", creds.AccessKeyID)
}

func TestSharedCredentialsProvider_EmptyProfileHonorsAWSProfile(t *testing.T) {
	t.Setenv("AWS_PROFILE", "named-profile")
	tmpFilename := tempCredentialsFile(t) // fixture only contains [default]
	p := NewSharedCredentialsProvider(tmpFilename, "")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}

func TestRefreshableSharedCredentialsProvider_Error(t *testing.T) {
	tmpFilename := tempCredentialsFile(t)
	p := NewRefreshableSharedCredentialsProvider(tmpFilename, "no-such-profile", time.Hour)
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}
