// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	logger             *zap.Logger
	config             *Config
	providers          []TokenProvider
	tokenProvider      TokenProvider
	done               chan struct{}
	wg                 sync.WaitGroup
	shutdownOnce       sync.Once
	minRefreshInterval time.Duration
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

	// Remove stale token from previous run/crash
	e.removeTokenFile()

	expiry, err := e.refreshToken(ctx)
	if err != nil {
		return fmt.Errorf("oidctoken: initial token fetch failed: %w", err)
	}

	e.done = make(chan struct{})
	e.wg.Go(func() {
		e.refreshLoop(expiry)
	})
	return nil
}

func (e *oidcTokenExtension) Shutdown(_ context.Context) error {
	e.shutdownOnce.Do(func() {
		if e.done != nil {
			close(e.done)
		}
	})
	e.wg.Wait()
	e.removeTokenFile()
	return nil
}

func (e *oidcTokenExtension) removeTokenFile() {
	if err := os.Remove(e.config.OutputTokenFile); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("Failed to remove token file", zap.Error(err))
	}
}

func (e *oidcTokenExtension) refreshLoop(expiry time.Time) {
	for {
		interval := time.Until(expiry) - refreshBuffer
		interval = max(interval, e.minRefreshInterval)
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			newExpiry, err := e.refreshToken(ctx)
			cancel()
			if err != nil {
				e.logger.Error("Token refresh failed, will retry",
					zap.Error(err),
					zap.Duration("retry_in", e.minRefreshInterval))
			} else {
				expiry = newExpiry
				e.logger.Debug("Token refreshed successfully",
					zap.String("provider", e.tokenProvider.Name()),
					zap.Time("next_expiry", expiry))
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
