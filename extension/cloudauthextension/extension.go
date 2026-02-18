// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/cloudauthextension"

import (
	"context"
	"errors"
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
	tokenFilePerms     = 0o600
	tokenFileName      = "cloudauth-token" //nolint:gosec // not a credential
)

type cloudAuthExtension struct {
	logger             *zap.Logger
	config             *Config
	tokenProvider      TokenProvider
	tokenFile          string
	done               chan struct{}
	wg                 sync.WaitGroup
	shutdownOnce       sync.Once
	minRefreshInterval time.Duration
}

var _ extension.Extension = (*cloudAuthExtension)(nil)

func (e *cloudAuthExtension) Start(ctx context.Context, _ component.Host) error {
	if e.config.TokenFile != "" {
		// User-managed token file: just point the env var to it
		if err := os.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", e.config.TokenFile); err != nil {
			return fmt.Errorf("cloudauth: set env var: %w", err)
		}
		e.logger.Info("Using user-managed token file", zap.String("path", e.config.TokenFile))
		return nil
	}

	// Auto-detect cloud provider
	ap := newAzureProvider(e.config.Audience)
	if !ap.IsAvailable(ctx) {
		return errors.New("cloudauth: no OIDC provider detected in current environment")
	}
	e.tokenProvider = ap
	e.logger.Info("Cloud auth provider detected", zap.String("provider", ap.Name()))

	tokenDir := e.config.TokenDir
	if tokenDir == "" {
		tokenDir = os.TempDir()
	}
	if err := os.MkdirAll(tokenDir, 0o755); err != nil {
		return fmt.Errorf("cloudauth: failed to create token directory: %w", err)
	}
	e.tokenFile = filepath.Join(tokenDir, tokenFileName)

	if err := os.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", e.tokenFile); err != nil {
		return fmt.Errorf("cloudauth: set env var: %w", err)
	}

	expiry, err := e.refreshToken(ctx)
	if err != nil {
		return fmt.Errorf("cloudauth: initial token fetch failed: %w", err)
	}

	e.done = make(chan struct{})
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.refreshLoop(expiry)
	}()

	return nil
}

func (e *cloudAuthExtension) Shutdown(_ context.Context) error {
	e.shutdownOnce.Do(func() {
		if e.done != nil {
			close(e.done)
		}
	})
	e.wg.Wait()
	if e.tokenFile != "" {
		os.Remove(e.tokenFile)
	}
	os.Unsetenv("AWS_WEB_IDENTITY_TOKEN_FILE")
	return nil
}

func (e *cloudAuthExtension) refreshLoop(expiry time.Time) {
	for {
		interval := time.Until(expiry) - refreshBuffer
		if interval < e.minRefreshInterval {
			interval = e.minRefreshInterval
		}

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

func (e *cloudAuthExtension) refreshToken(ctx context.Context) (time.Time, error) {
	token, ttl, err := e.tokenProvider.GetToken(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("get OIDC token from %s: %w", e.tokenProvider.Name(), err)
	}
	if err := os.WriteFile(e.tokenFile, []byte(token), tokenFilePerms); err != nil {
		return time.Time{}, fmt.Errorf("write token file: %w", err)
	}
	return time.Now().Add(ttl), nil
}
