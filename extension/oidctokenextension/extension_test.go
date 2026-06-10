// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStartWithProvider(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token")

	mp := &mockProvider{token: "test-token", expiry: time.Hour}
	cfg := &Config{OutputTokenFile: tokenFile}
	ext := &oidcTokenExtension{
		logger:             zap.NewNop(),
		config:             cfg,
		providers:          []TokenProvider{mp},
		minRefreshInterval: minRefreshInterval,
	}

	err := ext.Start(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, int32(1), mp.calls.Load())

	data, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	require.Equal(t, "test-token", string(data))

	require.NoError(t, ext.Shutdown(t.Context()))
}

func TestStartWithoutProvider(t *testing.T) {
	cfg := &Config{OutputTokenFile: "/tmp/token"}
	ext := &oidcTokenExtension{
		logger: zap.NewNop(),
		config: cfg,
	}

	err := ext.Start(t.Context(), nil)
	require.NoError(t, err)
}

func TestShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "oidc-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("test"), 0o600))

	ext := &oidcTokenExtension{
		logger: zap.NewNop(),
		config: &Config{OutputTokenFile: tokenFile},
		done:   make(chan struct{}),
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
	calls  atomic.Int32
}

func (m *mockProvider) GetToken(_ context.Context) (string, time.Duration, error) {
	m.calls.Add(1)
	return m.token, m.expiry, m.err
}

func (*mockProvider) IsAvailable(_ context.Context) bool { return true }
func (*mockProvider) Name() string                       { return "mock" }

func TestRefreshLoop(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "oidc-token")

	mp := &mockProvider{token: "refreshed-token", expiry: 50 * time.Millisecond}
	ext := &oidcTokenExtension{
		logger:             zap.NewNop(),
		config:             &Config{OutputTokenFile: tokenFile},
		tokenProvider:      mp,
		done:               make(chan struct{}),
		minRefreshInterval: 10 * time.Millisecond,
	}

	ext.wg.Go(func() {
		ext.refreshLoop(time.Now().Add(-time.Hour))
	})

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(tokenFile)
		return err == nil && string(data) == "refreshed-token"
	}, 5*time.Second, 10*time.Millisecond)

	close(ext.done)
	ext.wg.Wait()
}

func TestWriteFileAtomicPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token")

	require.NoError(t, os.WriteFile(tokenFile, []byte("old"), 0o666))

	require.NoError(t, writeFileAtomic(tokenFile, []byte("new-token")))

	data, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	require.Equal(t, "new-token", string(data))

	info, err := os.Stat(tokenFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(tokenFilePerms), info.Mode().Perm())
}

func TestRefreshLoopError(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "oidc-token")

	mp := &mockProvider{err: context.DeadlineExceeded}
	ext := &oidcTokenExtension{
		logger:             zap.NewNop(),
		config:             &Config{OutputTokenFile: tokenFile},
		tokenProvider:      mp,
		done:               make(chan struct{}),
		minRefreshInterval: 10 * time.Millisecond,
	}

	ext.wg.Go(func() {
		ext.refreshLoop(time.Now().Add(-time.Hour))
	})

	require.Eventually(t, func() bool { return mp.calls.Load() >= 2 }, 5*time.Second, 10*time.Millisecond)

	_, err := os.Stat(tokenFile)
	require.True(t, os.IsNotExist(err))

	close(ext.done)
	ext.wg.Wait()
}
