// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/awscloudwatchlogsprovisionerextension"

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension/extensionauth"
	"go.opentelemetry.io/collector/extension/extensioncapabilities"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var (
	_ component.Component             = (*provisionerExtension)(nil)
	_ extensionauth.HTTPClient        = (*provisionerExtension)(nil)
	_ extensioncapabilities.Dependent = (*provisionerExtension)(nil)
)

const streamRetryDelay = 150 * time.Millisecond

type cacheEntry struct {
	success   bool
	expiresAt time.Time // only used for failed entries
}

// inFailureBackoff reports whether the entry is a failed entry whose backoff
// has not yet expired.
func (ce cacheEntry) inFailureBackoff() bool {
	return !ce.success && time.Now().Before(ce.expiresAt)
}

// cwLogsClient abstracts the CloudWatch Logs API for testability.
type cwLogsClient interface {
	CreateLogGroup(ctx context.Context, logGroupName string) error
	CreateLogStream(ctx context.Context, logGroupName, logStreamName string) error
}

type provisionerExtension struct {
	logger *zap.Logger
	cfg    *Config

	// host is stored during Start() for lazy resolution of additional_auth.
	// Follows the same pattern as headers_setter extension.
	host   component.Host
	client cwLogsClient

	cache            sync.Map
	sfProvision      singleflight.Group
	sfCreateGroup    singleflight.Group
	streamRetryDelay time.Duration
}

func newExtension(logger *zap.Logger, cfg *Config) *provisionerExtension {
	return &provisionerExtension{
		logger:           logger,
		cfg:              cfg,
		streamRetryDelay: streamRetryDelay,
	}
}

func (e *provisionerExtension) Start(ctx context.Context, host component.Host) error {
	e.host = host

	client, err := newDefaultCWLogsClient(ctx, e.logger, &e.cfg.AWSSessionSettings)
	if err != nil {
		return fmt.Errorf("failed to create CW Logs client: %w", err)
	}
	e.client = client

	e.logger.Info(
		"awscloudwatchlogsprovisioner started",
		zap.String("region", e.cfg.Region),
	)
	return nil
}

func (e *provisionerExtension) Shutdown(_ context.Context) error {
	return nil
}

func (e *provisionerExtension) Dependencies() []component.ID {
	if e.cfg.AdditionalAuth != nil {
		return []component.ID{*e.cfg.AdditionalAuth}
	}
	return nil
}

// getAdditionalAuthExtension retrieves the configured additional auth extension if present.
// Returns nil if no additional auth is configured.
// Follows the same pattern as headers_setter:
// https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/v0.150.0/extension/headerssetterextension/extension.go
func (e *provisionerExtension) getAdditionalAuthExtension() (component.Component, error) {
	if e.cfg.AdditionalAuth == nil || e.host == nil {
		return nil, nil
	}
	ext := e.host.GetExtensions()[*e.cfg.AdditionalAuth]
	if ext == nil {
		return nil, fmt.Errorf("additional_auth extension %v not found", e.cfg.AdditionalAuth)
	}
	return ext, nil
}

func (e *provisionerExtension) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	transport := base

	ext, err := e.getAdditionalAuthExtension()
	if err != nil {
		return nil, err
	}
	if ext != nil {
		if httpClient, ok := ext.(extensionauth.HTTPClient); ok {
			transport, err = httpClient.RoundTripper(base)
			if err != nil {
				return nil, fmt.Errorf("failed to get RoundTripper from %v: %w", e.cfg.AdditionalAuth, err)
			}
		}
	}

	return &provisionerRoundTripper{
		base: transport,
		ext:  e,
	}, nil
}

type provisionerRoundTripper struct {
	base http.RoundTripper
	ext  *provisionerExtension
}

func (rt *provisionerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Read headers set by the otlphttp exporter (static) or headers_setter (dynamic).
	logGroup := req.Header.Get("x-aws-log-group")
	logStream := req.Header.Get("x-aws-log-stream")

	if logGroup != "" && logStream != "" {
		rt.ext.ensure(req.Context(), logGroup, logStream)

		resp, err := rt.base.RoundTrip(req)
		if err != nil {
			return resp, err
		}

		// If the CW OTLP endpoint returns 400 with "does not exist", evict the
		// success cache entry and return an error so the exporter retries
		// instead of treating the 400 as a permanent failure and dropping the
		// data. On a retry after the failure backoff expires, ensure()
		// re-provisions the log group/stream.
		if resp.StatusCode == http.StatusBadRequest {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && strings.Contains(string(body), "does not exist") {
				rt.ext.evictSuccessfulEntry(logGroup, logStream)
				return nil, fmt.Errorf("destination log group %q/stream %q does not exist, will re-provision on next retry", logGroup, logStream)
			}
			resp.Body = io.NopCloser(strings.NewReader(string(body)))
		}

		return resp, nil
	}

	return rt.base.RoundTrip(req)
}

func cacheKey(logGroup, logStream string) string {
	return logGroup + "\x00" + logStream
}

// ensure creates the log group and stream if not already cached.
// Returns true if the log group/stream is known to be provisioned (cache hit on
// success entry or newly provisioned). Returns false if provisioning failed or
// is within failure backoff.
// Uses singleflight to deduplicate concurrent creation attempts for the same key.
func (e *provisionerExtension) ensure(ctx context.Context, logGroup, logStream string) bool {
	key := cacheKey(logGroup, logStream)

	if entry, ok := e.cache.Load(key); ok {
		ce := entry.(cacheEntry)
		if ce.success {
			return true
		}
		if ce.inFailureBackoff() {
			return false
		}
	}

	_, _, _ = e.sfProvision.Do(key, func() (any, error) {
		// Double-check cache after acquiring singleflight.
		if entry, ok := e.cache.Load(key); ok {
			ce := entry.(cacheEntry)
			if ce.success || ce.inFailureBackoff() {
				return nil, nil
			}
		}

		err := e.provision(ctx, logGroup, logStream)
		if err != nil {
			// Don't cache failures caused by context cancellation — allow retry for provision
			if ctx.Err() != nil {
				return nil, nil
			}
			e.cache.Store(key, cacheEntry{expiresAt: time.Now().Add(e.cfg.LogsProvisionFailureBackoff)})
			e.logger.Warn(
				"Failed to create log group/stream",
				zap.String("logGroup", logGroup),
				zap.String("logStream", logStream),
				zap.Duration("backoff", e.cfg.LogsProvisionFailureBackoff),
				zap.Error(err),
			)
		} else {
			e.cache.Store(key, cacheEntry{success: true})
			e.logger.Debug(
				"Successfully provisioned log group/stream",
				zap.String("logGroup", logGroup),
				zap.String("logStream", logStream),
			)
		}
		return nil, nil
	})

	// Check final cache state after singleflight completes.
	if entry, ok := e.cache.Load(key); ok {
		return entry.(cacheEntry).success
	}
	return false
}

// provision creates the log stream (and log group if needed).
// Tries stream first — if the group doesn't exist, creates it and retries.
func (e *provisionerExtension) provision(ctx context.Context, logGroup, logStream string) error {
	err := e.client.CreateLogStream(ctx, logGroup, logStream)
	if err == nil {
		return nil
	}

	if !isNotFound(err) {
		return fmt.Errorf("CreateLogStream %q in %q: %w", logStream, logGroup, err)
	}

	e.logger.Debug(
		"Log group not found, creating",
		zap.String("logGroup", logGroup),
	)
	_, grpErr, _ := e.sfCreateGroup.Do(logGroup, func() (any, error) {
		// Use Background so the shared create is not canceled when the winning
		// request's context expires. The SDK HTTP client has its own timeout.
		return nil, e.client.CreateLogGroup(context.Background(), logGroup)
	})
	if grpErr != nil {
		return fmt.Errorf("CreateLogGroup %q: %w", logGroup, grpErr)
	}

	// Brief pause since the group may not be visible to CreateLogStream yet.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(e.streamRetryDelay):
	}

	if retryErr := e.client.CreateLogStream(ctx, logGroup, logStream); retryErr != nil {
		return fmt.Errorf("CreateLogStream %q in %q (retry): %w", logStream, logGroup, retryErr)
	}
	return nil
}

// evict removes the cache entry only if it was previously successful.
// Failed entries retain their backoff TTL.
func (e *provisionerExtension) evictSuccessfulEntry(logGroup, logStream string) {
	key := cacheKey(logGroup, logStream)
	if entry, ok := e.cache.Load(key); ok {
		if entry.(cacheEntry).success {
			e.cache.Delete(key)
		}
	}
}
