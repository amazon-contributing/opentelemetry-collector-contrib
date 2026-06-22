// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.uber.org/zap"
)

const (
	refreshBuffer      = 5 * time.Minute
	minRefreshInterval = 1 * time.Minute
	tokenFilePerms     = 0o400
)

type oidcTokenExtension struct {
	logger        *zap.Logger
	config        *Config
	providers     []TokenProvider
	tokenProvider TokenProvider
	done          chan struct{}
	// refreshCtx is the long-lived parent context for the background refresh
	// loop. It is derived from context.Background() (not Start's ctx, which may
	// be cancelled once Start returns) so the loop can outlive Start. Each
	// per-refresh timeout is derived from it, so canceling it via cancel on
	// Shutdown interrupts any in-flight token refresh.
	refreshCtx         context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	shutdownOnce       sync.Once
	minRefreshInterval time.Duration
	// wroteToken records whether this extension actually wrote the output token
	// file. It guards Shutdown so a no-op run (no provider detected) does not
	// delete a file at config.OutputTokenFile that this extension never wrote.
	// It is atomic because it is written from the background refresh goroutine
	// and read in Shutdown.
	wroteToken atomic.Bool
}

var _ extension.Extension = (*oidcTokenExtension)(nil)

func (e *oidcTokenExtension) Start(ctx context.Context, _ component.Host) error {
	for _, p := range e.providers {
		if p.IsAvailable(ctx) {
			e.tokenProvider = p
			break
		}
	}
	if e.tokenProvider == nil {
		e.logger.Warn("No OIDC provider detected, extension is a no-op")
		return nil
	}
	e.logger.Info("OIDC provider detected", zap.String("provider", e.tokenProvider.Name()))

	// Truncate any stale token from a previous run/crash. Truncating (rather
	// than deleting) keeps a zero-byte file in place so sigv4auth's lazy token
	// read does not fail before the fresh token is written.
	e.truncateTokenFile()

	expiry, err := e.refreshToken(ctx)
	if err != nil {
		return fmt.Errorf("oidctoken: initial token fetch failed: %w", err)
	}

	e.done = make(chan struct{})
	// Derive the refresh loop's parent context from context.Background() rather
	// than Start's ctx: the loop outlives Start, but cancel lets Shutdown
	// interrupt an in-flight refresh.
	e.refreshCtx, e.cancel = context.WithCancel(context.Background())
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.refreshLoop(expiry)
	}()
	return nil
}

func (e *oidcTokenExtension) Shutdown(ctx context.Context) error {
	e.shutdownOnce.Do(func() {
		if e.done != nil {
			close(e.done)
		}
		// Cancel the refresh loop's parent context so any in-flight token
		// refresh is interrupted instead of blocking on its own 30s timeout.
		if e.cancel != nil {
			e.cancel()
		}
	})
	// Bound the wait on the shutdown context so a refresh that does not honor
	// cancellation cannot block shutdown past its deadline.
	waited := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Only clear the token file if this extension actually wrote it. A no-op
	// run (no provider detected) must not touch a pre-existing file at
	// config.OutputTokenFile that it never owned. The file is truncated (not
	// deleted) so sigv4auth validation does not fail on the next startup.
	if e.wroteToken.Load() {
		e.truncateTokenFile()
	}
	return nil
}

func (e *oidcTokenExtension) truncateTokenFile() {
	if err := os.Truncate(e.config.OutputTokenFile, 0); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("Failed to truncate token file", zap.Error(err))
	}
}

// nextRefreshInterval computes how long to wait before the next token refresh.
// It guarantees the refresh is never scheduled after the token expires: the
// minRefreshInterval floor is only applied as an error/expiry retry backoff, not
// as a floor that could outlast a short TTL.
func (e *oidcTokenExtension) nextRefreshInterval(expiry time.Time) time.Duration {
	remaining := time.Until(expiry)
	if remaining <= 0 {
		// Token already expired (e.g. after a failed refresh): back off by the
		// minimum interval before retrying so we don't hammer the metadata endpoint.
		return e.minRefreshInterval
	}
	interval := remaining - refreshBuffer
	if interval >= e.minRefreshInterval {
		return interval
	}
	// TTL is too short to apply the normal refresh buffer / minimum interval
	// without scheduling the refresh after expiry. Refresh after a bounded delay
	// strictly shorter than the remaining TTL instead.
	e.logger.Warn("OIDC token TTL shorter than refresh buffer; scheduling an early refresh",
		zap.Duration("ttl_remaining", remaining),
		zap.Duration("refresh_buffer", refreshBuffer),
		zap.Duration("min_refresh_interval", e.minRefreshInterval))
	return remaining / 2
}

func (e *oidcTokenExtension) refreshLoop(expiry time.Time) {
	interval := e.nextRefreshInterval(expiry)
	for {
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			ctx, cancel := context.WithTimeout(e.refreshCtx, 30*time.Second)
			newExpiry, err := e.refreshToken(ctx)
			cancel()
			if err != nil {
				// Back off by the minimum interval before retrying so a failing
				// metadata endpoint is not hammered.
				interval = e.minRefreshInterval
				e.logger.Error("Token refresh failed, will retry",
					zap.Error(err),
					zap.Duration("retry_in", interval))
			} else {
				interval = e.nextRefreshInterval(newExpiry)
				e.logger.Debug("Token refreshed successfully",
					zap.String("provider", e.tokenProvider.Name()),
					zap.Time("next_expiry", newExpiry))
			}
		case <-e.done:
			timer.Stop()
			return
		}
	}
}

func (e *oidcTokenExtension) refreshToken(ctx context.Context) (time.Time, error) {
	token, ttl, err := e.tokenProvider.GetToken(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("get OIDC token from %s: %w", e.tokenProvider.Name(), err)
	}
	if err = writeFileAtomic(e.config.OutputTokenFile, []byte(token)); err != nil {
		return time.Time{}, fmt.Errorf("write token file: %w", err)
	}
	e.wroteToken.Store(true)
	return time.Now().Add(ttl), nil
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".oidctoken-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := f.Name()

	if _, err = f.Write(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err = f.Chmod(tokenFilePerms); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err = f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
