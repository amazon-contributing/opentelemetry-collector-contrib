// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStartWithTokenFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("test-token"), 0o600))

	cfg := &Config{TokenFile: tokenFile}
	ext := &cloudAuthExtension{
		logger: zap.NewNop(),
		config: cfg,
	}

	err := ext.Start(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, tokenFile, os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE"))
}

func TestStartWithoutProvider(t *testing.T) {
	cfg := &Config{}
	ext := &cloudAuthExtension{
		logger: zap.NewNop(),
		config: cfg,
	}

	err := ext.Start(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no OIDC provider detected")
}

func TestShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "cloudauth-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("test"), 0o600))

	ext := &cloudAuthExtension{
		logger:    zap.NewNop(),
		tokenFile: tokenFile,
		done:      make(chan struct{}),
	}

	err := ext.Shutdown(t.Context())
	require.NoError(t, err)
	_, err = os.Stat(tokenFile)
	require.True(t, os.IsNotExist(err))
}

func TestFactory(t *testing.T) {
	factory := NewFactory()
	require.NotNil(t, factory)

	cfg := factory.CreateDefaultConfig()
	require.NotNil(t, cfg)
	require.IsType(t, &Config{}, cfg)
}

// mockProvider implements TokenProvider for testing.
type mockProvider struct {
	token  string
	expiry time.Duration
	err    error
	calls  int
}

func (m *mockProvider) GetToken(_ context.Context) (string, time.Duration, error) {
	m.calls++
	return m.token, m.expiry, m.err
}

func (m *mockProvider) IsAvailable(_ context.Context) bool { return true }
func (m *mockProvider) Name() string                       { return "mock" }

func TestRefreshLoop(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, tokenFileName)

	mp := &mockProvider{token: "refreshed-token", expiry: 50 * time.Millisecond}
	ext := &cloudAuthExtension{
		logger:             zap.NewNop(),
		config:             &Config{},
		tokenProvider:      mp,
		tokenFile:          tokenFile,
		done:               make(chan struct{}),
		minRefreshInterval: 10 * time.Millisecond,
	}

	// Start loop with an already-expired token so it refreshes immediately.
	go ext.refreshLoop(time.Now().Add(-time.Hour))

	// Wait for token file to be written.
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(tokenFile)
		return err == nil && string(data) == "refreshed-token"
	}, 5*time.Second, 10*time.Millisecond)

	// Shutdown stops the goroutine.
	close(ext.done)
}

func TestRefreshLoopError(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, tokenFileName)

	mp := &mockProvider{err: context.DeadlineExceeded}
	ext := &cloudAuthExtension{
		logger:             zap.NewNop(),
		config:             &Config{},
		tokenProvider:      mp,
		tokenFile:          tokenFile,
		done:               make(chan struct{}),
		minRefreshInterval: 10 * time.Millisecond,
	}

	go ext.refreshLoop(time.Now().Add(-time.Hour))

	// Should retry on error.
	require.Eventually(t, func() bool { return mp.calls >= 2 }, 5*time.Second, 50*time.Millisecond)

	// Token file should not exist since all calls failed.
	_, err := os.Stat(tokenFile)
	require.True(t, os.IsNotExist(err))

	close(ext.done)
}
