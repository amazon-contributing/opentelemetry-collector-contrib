// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/cloudauthextension"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	logger        *zap.Logger
	config        *Config
	tokenProvider TokenProvider
	tokenFile     string
	done          chan struct{}
}

var _ extension.Extension = (*cloudAuthExtension)(nil)

func (e *cloudAuthExtension) Start(ctx context.Context, _ component.Host) error {
	var tp TokenProvider
	if e.config.TokenFile != "" {
		fp := newFileProvider(e.config.TokenFile)
		if !fp.IsAvailable(ctx) {
			return fmt.Errorf("cloudauth: token file %q does not exist", e.config.TokenFile)
		}
		tp = fp
	} else {
		ap := newAzureProvider()
		if !ap.IsAvailable(ctx) {
			return errors.New("cloudauth: no OIDC provider detected in current environment")
		}
		if e.config.STSResource != "" {
			ap.SetResource(e.config.STSResource)
		}
		tp = ap
	}
	e.tokenProvider = tp
	e.logger.Info("Cloud auth provider detected", zap.String("provider", tp.Name()))

	tokenDir := e.config.TokenDir
	if tokenDir == "" {
		tokenDir = os.TempDir()
	}
	if err := os.MkdirAll(tokenDir, 0755); err != nil {
		return fmt.Errorf("cloudauth: failed to create token directory: %w", err)
	}
	e.tokenFile = filepath.Join(tokenDir, tokenFileName)

	os.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", e.tokenFile)

	expiry, err := e.refreshToken(ctx)
	if err != nil {
		return fmt.Errorf("cloudauth: initial token fetch failed: %w", err)
	}

	e.done = make(chan struct{})
	go e.refreshLoop(expiry)

	return nil
}

func (e *cloudAuthExtension) Shutdown(_ context.Context) error {
	if e.done != nil {
		close(e.done)
	}
	if e.tokenFile != "" {
		os.Remove(e.tokenFile)
	}
	return nil
}

func (e *cloudAuthExtension) refreshLoop(expiry time.Time) {
	for {
		interval := time.Until(expiry) - refreshBuffer
		if interval < minRefreshInterval {
			interval = minRefreshInterval
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
					zap.Duration("retry_in", minRefreshInterval))
				time.Sleep(minRefreshInterval)
			} else {
				expiry = newExpiry
				e.logger.Info("Token refreshed successfully",
					zap.String("provider", e.tokenProvider.Name()))
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
