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

func TestSharedCredentialsProvider_MissingProfile(t *testing.T) {
	tmpFilename := tempCredentialsFile(t)
	p := newSharedCredentialsProvider(tmpFilename, "no-such-profile")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}

func TestSharedCredentialsProvider_MissingFileDefaultProfile(t *testing.T) {
	isolateAWSEnv(t)
	p := newSharedCredentialsProvider("/nonexistent", "default")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err, "v2 SDK errors on missing file even for the default profile")
}

func TestSharedCredentialsProvider_MissingFileNonDefaultProfile(t *testing.T) {
	isolateAWSEnv(t)
	p := newSharedCredentialsProvider("/nonexistent", "named-profile")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}

func TestSharedCredentialsProvider_EmptyProfileDefaultsToDefault(t *testing.T) {
	t.Setenv("AWS_PROFILE", "")
	tmpFilename := tempCredentialsFile(t)
	p := newSharedCredentialsProvider(tmpFilename, "")
	creds, err := p.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "ASIAIKJ", creds.AccessKeyID)
}

func TestSharedCredentialsProvider_EmptyProfileHonorsAWSProfile(t *testing.T) {
	t.Setenv("AWS_PROFILE", "named-profile")
	tmpFilename := tempCredentialsFile(t) // fixture only contains [default]
	p := newSharedCredentialsProvider(tmpFilename, "")
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}

func TestRefreshableSharedCredentialsProvider(t *testing.T) {
	tmpFilename := tempCredentialsFile(t)

	provider := NewRefreshableSharedCredentialsProvider(tmpFilename, testProfile, 500*time.Millisecond)

	got, err := provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "o1rLD3ykKN09", got.SecretAccessKey)
	assert.False(t, got.Expired())

	// Rotate the file contents.
	rotated, err := os.ReadFile(filepath.Join("testdata", "credential_rotate"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpFilename, rotated, 0o600))

	// Credentials must expire once the expiry window elapses.
	assert.Eventually(t, got.Expired, time.Second, 25*time.Millisecond,
		"credentials did not expire after the expiry window elapsed")

	got, err = provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "o1rLDaaaccc", got.SecretAccessKey)
	assert.False(t, got.Expired())
}

func TestRefreshableSharedCredentialsProvider_Error(t *testing.T) {
	tmpFilename := tempCredentialsFile(t)
	p := NewRefreshableSharedCredentialsProvider(tmpFilename, "no-such-profile", time.Hour)
	_, err := p.Retrieve(t.Context())
	require.Error(t, err)
}
