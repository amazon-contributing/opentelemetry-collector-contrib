// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStartWithProvider(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token")

	mp := &mockProvider{token: "test-token", expiry: time.Hour, available: true}
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

func TestShutdownNoProviderPreservesExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "oidc-token")
	// A file owned by something else (e.g. a prior sigv4auth setup) already
	// exists at the configured path.
	require.NoError(t, os.WriteFile(tokenFile, []byte("not-ours"), 0o600))

	mp := &mockProvider{available: false}
	ext := &oidcTokenExtension{
		logger:             zap.NewNop(),
		config:             &Config{OutputTokenFile: tokenFile},
		providers:          []TokenProvider{mp},
		minRefreshInterval: minRefreshInterval,
	}

	// No provider is available, so Start is a no-op and never writes the file.
	require.NoError(t, ext.Start(t.Context(), nil))
	require.False(t, ext.wroteToken.Load())

	require.NoError(t, ext.Shutdown(t.Context()))

	// The pre-existing file this extension never wrote must be left untouched.
	data, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	require.Equal(t, "not-ours", string(data))
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
	// Simulate a run where the extension actually wrote the token file, so
	// Shutdown is responsible for cleaning it up.
	ext.wroteToken.Store(true)

	err := ext.Shutdown(t.Context())
	require.NoError(t, err)
	// The token file this extension wrote is truncated (not deleted) on
	// shutdown, so sigv4auth validation does not fail on the next startup.
	info, err := os.Stat(tokenFile)
	require.NoError(t, err)
	require.Zero(t, info.Size())
}

// blockingProvider.GetToken blocks until release is closed and deliberately
// ignores its context, simulating a token refresh in flight that does not
// honor cancellation. It lets the context-bounded wait in Shutdown be
// exercised deterministically without any real 30s timeout.
type blockingProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingProvider) GetToken(_ context.Context) (string, time.Duration, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return "blocking-token", time.Hour, nil
}

func (*blockingProvider) IsAvailable(_ context.Context) bool { return true }
func (*blockingProvider) Name() string                       { return "blocking" }

func TestShutdownBoundedByContext(t *testing.T) {
	t.Run("returns ctx.Err while refresh is in flight", func(t *testing.T) {
		tmpDir := t.TempDir()
		tokenFile := filepath.Join(tmpDir, "oidc-token")

		bp := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
		ext := &oidcTokenExtension{
			logger:             zap.NewNop(),
			config:             &Config{OutputTokenFile: tokenFile},
			tokenProvider:      bp,
			done:               make(chan struct{}),
			minRefreshInterval: time.Millisecond,
		}
		ext.refreshCtx, ext.cancel = context.WithCancel(t.Context())

		// Start the refresh loop with an already-expired token so it attempts a
		// refresh immediately; GetToken then blocks, leaving a refresh in flight.
		ext.wg.Add(1)
		go func() {
			defer ext.wg.Done()
			ext.refreshLoop(time.Now().Add(-time.Hour))
		}()

		select {
		case <-bp.started:
		case <-time.After(5 * time.Second):
			t.Fatal("refresh never started")
		}

		// With a refresh stuck in GetToken, Shutdown must honor its short
		// deadline instead of blocking up to the 30s per-refresh timeout.
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := ext.Shutdown(ctx)
		elapsed := time.Since(start)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, elapsed, 5*time.Second,
			"Shutdown must return near its deadline, not block on the in-flight refresh")

		// Release the blocked refresh so the loop exits and no goroutine leaks.
		close(bp.release)
		ext.wg.Wait()
	})

	t.Run("normal shutdown returns nil and removes written token file", func(t *testing.T) {
		tmpDir := t.TempDir()
		tokenFile := filepath.Join(tmpDir, "oidc-token")
		require.NoError(t, os.WriteFile(tokenFile, []byte("written"), 0o600))

		ext := &oidcTokenExtension{
			logger: zap.NewNop(),
			config: &Config{OutputTokenFile: tokenFile},
			done:   make(chan struct{}),
		}
		ext.wroteToken.Store(true)
		ext.refreshCtx, ext.cancel = context.WithCancel(t.Context())

		// No refresh is in flight, so the bounded wait completes immediately and
		// Shutdown still clears the token file it wrote (truncated to zero bytes).
		require.NoError(t, ext.Shutdown(t.Context()))
		info, err := os.Stat(tokenFile)
		require.NoError(t, err)
		require.Zero(t, info.Size())
	})
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
	token     string
	expiry    time.Duration
	err       error
	available bool
	calls     atomic.Int32
}

func (m *mockProvider) GetToken(_ context.Context) (string, time.Duration, error) {
	m.calls.Add(1)
	return m.token, m.expiry, m.err
}

func (m *mockProvider) IsAvailable(_ context.Context) bool { return m.available }
func (*mockProvider) Name() string                         { return "mock" }

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
	refreshCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ext.refreshCtx, ext.cancel = refreshCtx, cancel

	ext.wg.Add(1)
	go func() {
		defer ext.wg.Done()
		ext.refreshLoop(time.Now().Add(-time.Hour))
	}()

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(tokenFile)
		return err == nil && string(data) == "refreshed-token"
	}, 5*time.Second, 10*time.Millisecond)

	close(ext.done)
	ext.wg.Wait()
}

func TestNextRefreshInterval(t *testing.T) {
	ext := &oidcTokenExtension{
		logger:             zap.NewNop(),
		minRefreshInterval: minRefreshInterval,
	}

	t.Run("long TTL subtracts refresh buffer", func(t *testing.T) {
		// 1h TTL -> refresh refreshBuffer before expiry.
		interval := ext.nextRefreshInterval(time.Now().Add(time.Hour))
		require.InDelta(t, (time.Hour - refreshBuffer).Seconds(), interval.Seconds(), 1)
	})

	t.Run("short sub-minute TTL refreshes before expiry", func(t *testing.T) {
		// 30s TTL is shorter than refreshBuffer+minRefreshInterval. The refresh
		// must be scheduled strictly before expiry, not after the minimum
		// interval (which would land 30s past expiry).
		ttl := 30 * time.Second
		interval := ext.nextRefreshInterval(time.Now().Add(ttl))
		require.Less(t, interval, ttl, "refresh must be scheduled before the token expires")
		require.Positive(t, interval)
	})

	t.Run("already expired backs off by minimum interval", func(t *testing.T) {
		interval := ext.nextRefreshInterval(time.Now().Add(-time.Minute))
		require.Equal(t, minRefreshInterval, interval)
	})
}

func TestWriteFileAtomicPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token")

	require.NoError(t, os.WriteFile(tokenFile, []byte("old"), 0o600))

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
	refreshCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ext.refreshCtx, ext.cancel = refreshCtx, cancel

	ext.wg.Add(1)
	go func() {
		defer ext.wg.Done()
		ext.refreshLoop(time.Now().Add(-time.Hour))
	}()

	require.Eventually(t, func() bool { return mp.calls.Load() >= 2 }, 5*time.Second, 10*time.Millisecond)

	_, err := os.Stat(tokenFile)
	require.True(t, os.IsNotExist(err))

	close(ext.done)
	ext.wg.Wait()
}
