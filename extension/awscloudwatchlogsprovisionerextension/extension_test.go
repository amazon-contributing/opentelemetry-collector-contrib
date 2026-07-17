// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.uber.org/zap/zaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

// --- Mock CW Logs client ---

type mockCWLogsClient struct {
	createGroupErr  error
	createStreamErr error
	groupCalls      atomic.Int32
	streamCalls     atomic.Int32
	// Optional hooks. When set they take precedence over the fixed errors
	// and receive the 1-based call count.
	createGroupFn  func(call int32) error
	createStreamFn func(call int32) error
}

func (m *mockCWLogsClient) CreateLogGroup(_ context.Context, _ string) error {
	call := m.groupCalls.Add(1)
	if m.createGroupFn != nil {
		return m.createGroupFn(call)
	}
	return m.createGroupErr
}

func (m *mockCWLogsClient) CreateLogStream(_ context.Context, _, _ string) error {
	call := m.streamCalls.Add(1)
	if m.createStreamFn != nil {
		return m.createStreamFn(call)
	}
	return m.createStreamErr
}

// --- Mock inner auth ---

type mockHTTPClient struct {
	component.StartFunc
	component.ShutdownFunc
}

func (m *mockHTTPClient) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	return base, nil
}

// mockAuthWithHeader simulates an auth extension that adds a specific header
// (e.g., sigv4auth adding Authorization). Used to verify auth chaining.
type mockAuthWithHeader struct {
	component.StartFunc
	component.ShutdownFunc
	headerKey   string
	headerValue string
}

func (m *mockAuthWithHeader) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req2 := req.Clone(req.Context())
		req2.Header.Set(m.headerKey, m.headerValue)
		return base.RoundTrip(req2)
	}), nil
}

// --- Mock host ---

type mockHost struct {
	extensions map[component.ID]component.Component
}

func (h *mockHost) GetExtensions() map[component.ID]component.Component {
	return h.extensions
}

// --- Helper to build extension ---

func newTestExtension(t *testing.T, cfg *Config, mockClient *mockCWLogsClient) *provisionerExtension {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)
	ext.client = mockClient
	ext.streamRetryDelay = 1 * time.Millisecond
	return ext
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// --- Tests ---

func TestRoundTripper_StaticHeaders(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	var capturedReq *http.Request
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedReq = req
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/static/my-group")
	req.Header.Set("x-aws-log-stream", "my-stream")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	assert.Equal(t, "/static/my-group", capturedReq.Header.Get("x-aws-log-group"))
	assert.Equal(t, "my-stream", capturedReq.Header.Get("x-aws-log-stream"))
	// Stream-first: CreateLogStream succeeds so CreateLogGroup is not called
	assert.Equal(t, int32(0), mockClient.groupCalls.Load())
	assert.Equal(t, int32(1), mockClient.streamCalls.Load())
}

// Test: no log group at all — request passes through without provisioning
func TestRoundTripper_NoLogGroup_PassesThrough(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	// No x-aws-log-group header

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	assert.Equal(t, int32(0), mockClient.groupCalls.Load(), "should not provision when no log group header")
}

// Test: missing log stream — skips provisioning (both headers required)
func TestRoundTripper_MissingStream_SkipsProvisioning(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/my/group")
	// No x-aws-log-stream header — both required for provisioning

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	assert.Equal(t, int32(0), mockClient.streamCalls.Load(), "should not provision when stream header missing")
}

// newTest400RoundTripper wraps ext around a base transport that always
// returns 400 with the given body.
func newTest400RoundTripper(t *testing.T, ext *provisionerExtension, respBody string) http.RoundTripper {
	t.Helper()
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}
	rt, err := ext.RoundTripper(roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(respBody)),
		}, nil
	}))
	require.NoError(t, err)
	return rt
}

func newLogsRequest(logGroup string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", logGroup)
	req.Header.Set("x-aws-log-stream", "default")
	return req
}

const doesNotExistBody = `{"message":"The specified log group does not exist."}`

// Test: 400 with "does not exist" evicts cache and returns error for retry
func TestRoundTripper_400DoesNotExist_EvictsAndReturnsError(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)
	rt := newTest400RoundTripper(t, ext, doesNotExistBody)

	// Provisions, gets 400, evicts, returns error for retry
	resp, err := rt.RoundTrip(newLogsRequest("/test/group"))
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Equal(t, int32(1), mockClient.streamCalls.Load())
}

// Test: 400 without "does not exist" does NOT evict cache
func TestRoundTripper_400OtherError_NoEviction(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)
	rt := newTest400RoundTripper(t, ext, `{"message":"Invalid log format"}`)

	_, err := rt.RoundTrip(newLogsRequest("/test/group"))
	require.NoError(t, err)

	// Only initial provision, no re-provision
	assert.Equal(t, int32(1), mockClient.streamCalls.Load(), "should not re-provision for non-existence 400")
}

// Test: 400 "does not exist" with a failed cache entry returns an error for
// retry but preserves the failure backoff (no immediate re-provision).
func TestRoundTripper_400DoesNotExist_FailedEntry_PreservesBackoff(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	mockClient := &mockCWLogsClient{
		createStreamErr: notFoundErr,
		createGroupErr:  errors.New("access denied"),
	}
	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, mockClient)
	rt := newTest400RoundTripper(t, ext, doesNotExistBody)

	// First call: ensure fails (access denied), caches failure entry. The 400
	// returns an error so the exporter retries instead of dropping.
	_, err := rt.RoundTrip(newLogsRequest("/test/group"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Equal(t, int32(1), mockClient.groupCalls.Load())

	// Second call (exporter retry during backoff): failure entry not evicted,
	// no re-provision attempt, still an error for the next retry.
	_, err = rt.RoundTrip(newLogsRequest("/test/group"))
	require.Error(t, err)
	assert.Equal(t, int32(1), mockClient.groupCalls.Load(), "backoff should suppress re-provisioning")
}

// Test: provisioning interrupted by context cancellation caches nothing, and
// the resulting 400 "does not exist" still returns an error to trigger retry
// (previously this state silently dropped the data).
func TestRoundTripper_400DoesNotExist_NoCacheEntry_ReturnsError(t *testing.T) {
	mockClient := &mockCWLogsClient{createStreamErr: context.Canceled}
	ext := newTestExtension(t, &Config{}, mockClient)
	rt := newTest400RoundTripper(t, ext, doesNotExistBody)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := newLogsRequest("/test/no-cache").WithContext(ctx)

	resp, err := rt.RoundTrip(req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")

	// The interrupted provision must not have cached anything.
	_, cached := ext.cache.Load(cacheKey("/test/no-cache", "default"))
	assert.False(t, cached, "canceled provision should not cache a failure entry")
}

func TestEvictSuccessfulEntry(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)

	t.Run("evicts success entry", func(t *testing.T) {
		ext.cache.Store(cacheKey("/group", "stream"), cacheEntry{success: true})
		ext.evictSuccessfulEntry("/group", "stream")

		_, loaded := ext.cache.Load(cacheKey("/group", "stream"))
		assert.False(t, loaded, "success entry should be evicted")
	})

	t.Run("preserves failed entry", func(t *testing.T) {
		ext.cache.Store(cacheKey("/group2", "stream"), cacheEntry{
			success:   false,
			expiresAt: time.Now().Add(time.Minute),
		})
		ext.evictSuccessfulEntry("/group2", "stream")

		_, loaded := ext.cache.Load(cacheKey("/group2", "stream"))
		assert.True(t, loaded, "failed entry should NOT be evicted")
	})

	t.Run("no-op when entry missing", func(_ *testing.T) {
		ext.evictSuccessfulEntry("/nonexistent", "stream")
		// No panic, no-op
	})
}

func TestEnsureProvisioned_Success(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)

	ext.ensure(t.Context(), "/test/group", "default")

	// Stream-first: CreateLogStream succeeds, no group creation needed
	assert.Equal(t, int32(0), mockClient.groupCalls.Load())
	assert.Equal(t, int32(1), mockClient.streamCalls.Load())

	// Second call should hit cache
	ext.ensure(t.Context(), "/test/group", "default")
	assert.Equal(t, int32(0), mockClient.groupCalls.Load(), "should not create again after cache hit")
	assert.Equal(t, int32(1), mockClient.streamCalls.Load(), "should not create again after cache hit")
}

func TestEnsureProvisioned_FailureThenBackoff(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	mockClient := &mockCWLogsClient{
		createStreamErr: notFoundErr,
		createGroupErr:  errors.New("throttled"),
	}
	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, mockClient)

	ext.ensure(t.Context(), "/test/group", "default")
	// Stream fails (not found) → group creation attempted → fails (throttled)
	assert.Equal(t, int32(1), mockClient.groupCalls.Load())

	ext.ensure(t.Context(), "/test/group", "default")
	assert.Equal(t, int32(1), mockClient.groupCalls.Load(), "should not retry during backoff")
}

func TestEnsureProvisioned_Singleflight(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ext.ensure(t.Context(), "/test/singleflight", "default")
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), mockClient.streamCalls.Load(), "singleflight should dedup concurrent creation")
}

func TestStart_StoresHost(t *testing.T) {
	authID := component.MustNewID("sigv4auth")
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "us-east-1", LocalMode: true},
		AdditionalAuth:     &authID,
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)

	host := &mockHost{
		extensions: map[component.ID]component.Component{
			authID: &mockHTTPClient{},
		},
	}

	err := ext.Start(t.Context(), host)
	require.NoError(t, err)
	assert.NotNil(t, ext.host)
}

func TestRoundTripper_MissingAdditionalAuth(t *testing.T) {
	authID := component.MustNewID("sigv4auth")
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "us-east-1", LocalMode: true},
		AdditionalAuth:     &authID,
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)

	host := &mockHost{extensions: map[component.ID]component.Component{}}
	err := ext.Start(t.Context(), host)
	require.NoError(t, err)

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	_, err = ext.RoundTripper(base)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestChainingWithAdditionalAuth verifies that the extension properly chains
// with an additional auth extension, ensuring both the provisioner's headers
// (x-aws-log-group) and the inner auth's modifications are present in the request.
func TestChainingWithAdditionalAuth(t *testing.T) {
	authID := component.MustNewID("sigv4auth")
	mockClient := &mockCWLogsClient{}

	ext := newTestExtension(t, &Config{
		AdditionalAuth: &authID,
	}, mockClient)

	mockAuth := &mockAuthWithHeader{
		headerKey:   "Authorization",
		headerValue: "AWS4-HMAC-SHA256 Credential=...",
	}
	ext.host = &mockHost{
		extensions: map[component.ID]component.Component{
			authID: mockAuth,
		},
	}

	var capturedReq *http.Request
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedReq = req
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/my-service")
	req.Header.Set("x-aws-log-stream", "default")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	assert.Equal(t, "/test/my-service", capturedReq.Header.Get("x-aws-log-group"))
	assert.Equal(t, "default", capturedReq.Header.Get("x-aws-log-stream"))
	assert.Equal(t, "AWS4-HMAC-SHA256 Credential=...", capturedReq.Header.Get("Authorization"))
}

func TestDependencies(t *testing.T) {
	authID := component.MustNewID("sigv4auth")

	ext := newExtension(zaptest.NewLogger(t), &Config{AdditionalAuth: &authID})
	assert.Equal(t, []component.ID{authID}, ext.Dependencies())

	ext2 := newExtension(zaptest.NewLogger(t), &Config{})
	assert.Nil(t, ext2.Dependencies())
}

func TestEnsureProvisioned_DifferentKeysIndependent(t *testing.T) {
	mockClient := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, mockClient)

	ext.ensure(t.Context(), "/test/service-a", "default")
	ext.ensure(t.Context(), "/test/service-b", "default")

	// Stream-first: both streams succeed without needing group creation
	assert.Equal(t, int32(0), mockClient.groupCalls.Load())
	assert.Equal(t, int32(2), mockClient.streamCalls.Load(), "different keys should create independently")
}

func TestFailureBackoff_ExpiresAndRetries(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	mockClient := &mockCWLogsClient{
		createStreamErr: notFoundErr,
		createGroupErr:  errors.New("throttled"),
	}
	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 1 * time.Second,
	}, mockClient)

	ext.ensure(t.Context(), "/test/group", "default")
	assert.Equal(t, int32(1), mockClient.groupCalls.Load())

	time.Sleep(1100 * time.Millisecond)

	ext.ensure(t.Context(), "/test/group", "default")
	assert.Equal(t, int32(2), mockClient.groupCalls.Load(), "should retry after backoff expires")
}

func TestProvision_StreamRetrySucceedsAfterGroupCreate(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	mockClient := &mockCWLogsClient{
		// Initial CreateLogStream hits NotFound; the retry after the delay succeeds.
		createStreamFn: func(call int32) error {
			if call == 1 {
				return notFoundErr
			}
			return nil
		},
	}

	ext := newTestExtension(t, &Config{}, mockClient)
	result := ext.ensure(t.Context(), "/test/group", "stream-b")

	assert.True(t, result)
	assert.Equal(t, int32(2), mockClient.streamCalls.Load())
	assert.Equal(t, int32(1), mockClient.groupCalls.Load())
}

func TestProvision_StreamRetryFailsAfterGroupCreate(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	mockClient := &mockCWLogsClient{
		createStreamErr: notFoundErr,
	}

	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, mockClient)
	result := ext.ensure(t.Context(), "/test/group", "stream-c")

	assert.False(t, result)
	// 1 initial + 1 retry after delay
	assert.Equal(t, int32(2), mockClient.streamCalls.Load())
}

func TestProvision_CreateLogGroupSingleflighted(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	var streamExists atomic.Bool
	mockClient := &mockCWLogsClient{
		createGroupFn: func(_ int32) error {
			time.Sleep(50 * time.Millisecond)
			streamExists.Store(true)
			return nil
		},
		createStreamFn: func(_ int32) error {
			if streamExists.Load() {
				return nil
			}
			return notFoundErr
		},
	}

	ext := newTestExtension(t, &Config{}, mockClient)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(stream string) {
			defer wg.Done()
			result := ext.ensure(t.Context(), "/test/group", stream)
			assert.True(t, result)
		}(fmt.Sprintf("stream-%d", i))
	}
	wg.Wait()

	assert.Equal(t, int32(1), mockClient.groupCalls.Load(), "concurrent provisions for the same group should share one CreateLogGroup call")
}
