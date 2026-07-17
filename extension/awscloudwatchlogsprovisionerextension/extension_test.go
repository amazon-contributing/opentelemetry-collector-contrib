// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.uber.org/zap/zaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

// --- Helper to build extension ---

func newTestExtension(t *testing.T, cfg *Config, client cwLogsClient) *provisionerExtension {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)
	ext.client = client
	ext.streamRetryDelay = 1 * time.Millisecond
	ext.retention.batchInterval = 50 * time.Millisecond
	ext.retention.retryBaseDelay = 10 * time.Millisecond
	ext.retention.Start(client)
	t.Cleanup(func() { ext.retention.Stop() })
	return ext
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// --- Tests ---

func TestRoundTripper_StaticHeaders(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/static/my-group", "my-stream").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)
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
	m.AssertCalled(t, "CreateLogStream", "/static/my-group", "my-stream")
	m.AssertNotCalled(t, "CreateLogGroup", mock.Anything, mock.Anything)
}

func TestRoundTripper_NoLogGroup_PassesThrough(t *testing.T) {
	m := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	m.AssertNotCalled(t, "CreateLogStream", mock.Anything, mock.Anything)
}

func TestRoundTripper_MissingStream_SkipsProvisioning(t *testing.T) {
	m := &mockCWLogsClient{}
	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/my/group")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	m.AssertNotCalled(t, "CreateLogStream", mock.Anything, mock.Anything)
}

func TestRoundTripper_400DoesNotExist_EvictsAndReturnsError(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"message":"The specified log group does not exist."}`)),
		}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/group")
	req.Header.Set("x-aws-log-stream", "default")

	resp, err := rt.RoundTrip(req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestRoundTripper_400DoesNotExist_ForgetsRetentionDedup(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	// Simulate a previously applied retention policy for the group.
	ext.retention.cache.Store("/test/group", time.Time{})

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"message":"The specified log group does not exist."}`)),
		}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/group")
	req.Header.Set("x-aws-log-stream", "default")

	_, err = rt.RoundTrip(req)
	require.Error(t, err)

	_, loaded := ext.retention.cache.Load("/test/group")
	assert.False(t, loaded, "retention dedup entry should be cleared on 400 does-not-exist")
}

func TestRoundTripper_400OtherError_NoEviction(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"message":"Invalid log format"}`)),
		}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/group")
	req.Header.Set("x-aws-log-stream", "default")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	m.AssertNumberOfCalls(t, "CreateLogStream", 1)
}

func TestRoundTripper_400DoesNotExist_FailedEntry_NoEviction(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(notFoundErr)
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(errors.New("access denied"))
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"message":"The specified log group does not exist."}`)),
		}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/group")
	req.Header.Set("x-aws-log-stream", "default")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)
}

func TestEvictSuccessfulEntry(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)
	ext := newTestExtension(t, &Config{}, m)

	t.Run("evicts success entry", func(t *testing.T) {
		ext.cache.Store(cacheKey("/group", "stream"), cacheEntry{success: true})
		ext.evictSuccessfulEntry("/group", "stream")
		_, loaded := ext.cache.Load(cacheKey("/group", "stream"))
		assert.False(t, loaded)
	})

	t.Run("preserves failed entry", func(t *testing.T) {
		ext.cache.Store(cacheKey("/group2", "stream"), cacheEntry{expiresAt: time.Now().Add(time.Minute)})
		ext.evictSuccessfulEntry("/group2", "stream")
		_, loaded := ext.cache.Load(cacheKey("/group2", "stream"))
		assert.True(t, loaded)
	})

	t.Run("no-op when entry missing", func(_ *testing.T) {
		ext.evictSuccessfulEntry("/nonexistent", "stream")
	})
}

func TestEnsureProvisioned_Success(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)

	ext.ensure(t.Context(), "/test/group", "default", "")
	m.AssertCalled(t, "CreateLogStream", "/test/group", "default")
	m.AssertNotCalled(t, "CreateLogGroup", mock.Anything, mock.Anything)

	ext.ensure(t.Context(), "/test/group", "default", "")
	m.AssertNumberOfCalls(t, "CreateLogStream", 1)
}

func TestEnsureProvisioned_FailureThenBackoff(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(notFoundErr)
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(errors.New("throttled"))
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, m)

	ext.ensure(t.Context(), "/test/group", "default", "")
	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)

	ext.ensure(t.Context(), "/test/group", "default", "")
	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)
}

func TestEnsureProvisioned_Singleflight(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/singleflight", "default").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ext.ensure(t.Context(), "/test/singleflight", "default", "")
		}()
	}
	wg.Wait()

	m.AssertNumberOfCalls(t, "CreateLogStream", 1)
}

func TestStart_StoresHost(t *testing.T) {
	authID := component.MustNewID("sigv4auth")
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "us-east-1", LocalMode: true},
		AdditionalAuth:     &authID,
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)

	host := &mockHost{
		extensions: map[component.ID]component.Component{authID: &mockHTTPClient{}},
	}

	err := ext.Start(t.Context(), host)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ext.Shutdown(t.Context()) })
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
	t.Cleanup(func() { _ = ext.Shutdown(t.Context()) })

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	_, err = ext.RoundTripper(base)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestChainingWithAdditionalAuth(t *testing.T) {
	authID := component.MustNewID("sigv4auth")
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/my-service", "default").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{AdditionalAuth: &authID}, m)

	mockAuth := &mockAuthWithHeader{
		headerKey:   "Authorization",
		headerValue: "AWS4-HMAC-SHA256 Credential=...",
	}
	ext.host = &mockHost{
		extensions: map[component.ID]component.Component{authID: mockAuth},
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
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", mock.Anything, mock.Anything).Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)

	ext.ensure(t.Context(), "/test/service-a", "default", "")
	ext.ensure(t.Context(), "/test/service-b", "default", "")

	m.AssertNumberOfCalls(t, "CreateLogStream", 2)
	m.AssertNotCalled(t, "CreateLogGroup", mock.Anything, mock.Anything)
}

func TestFailureBackoff_ExpiresAndRetries(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(notFoundErr)
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(errors.New("throttled"))
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 1 * time.Second,
	}, m)

	ext.ensure(t.Context(), "/test/group", "default", "")
	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)

	time.Sleep(1100 * time.Millisecond)

	ext.ensure(t.Context(), "/test/group", "default", "")
	m.AssertNumberOfCalls(t, "CreateLogGroup", 2)
}

func TestProvision_LogGroupClass(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "default").Return(notFoundErr).Once()
	m.On("CreateLogStream", "/test/group", "default").Return(nil)
	m.On("CreateLogGroup", "/test/group", types.LogGroupClassInfrequentAccess).Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)

	ext.ensure(t.Context(), "/test/group", "default", types.LogGroupClassInfrequentAccess)

	m.AssertCalled(t, "CreateLogGroup", "/test/group", types.LogGroupClassInfrequentAccess)
	m.AssertNumberOfCalls(t, "CreateLogStream", 2)
}

func TestParseLogGroupClass(t *testing.T) {
	tests := []struct {
		input    string
		expected types.LogGroupClass
	}{
		{"", ""},
		{"STANDARD", types.LogGroupClassStandard},
		{"INFREQUENT_ACCESS", types.LogGroupClassInfrequentAccess},
		{"standard", types.LogGroupClassStandard},                  // case-insensitive
		{"Infrequent_Access", types.LogGroupClassInfrequentAccess}, // case-insensitive
		{"DELIVERY", ""}, // valid API value, not supported for creation
		{"invalid", ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, parseLogGroupClass(tt.input), "input: %q", tt.input)
	}
}

func TestIsOperationAborted(t *testing.T) {
	abortedErr := &types.OperationAbortedException{Message: aws.String("concurrent operation")}
	assert.True(t, isOperationAborted(abortedErr))
	assert.False(t, isOperationAborted(errors.New("some other error")))
	assert.False(t, isOperationAborted(nil))
}

func TestRoundTripper_RetentionAndLogClassHeaders(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/my-group", "my-stream").Return(notFoundErr).Once()
	m.On("CreateLogStream", "/test/my-group", "my-stream").Return(nil)
	m.On("CreateLogGroup", "/test/my-group", types.LogGroupClassInfrequentAccess).Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/my-group", int32(90)).Return(nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/my-group")
	req.Header.Set("x-aws-log-stream", "my-stream")
	req.Header.Set("x-aws-log-retention-days", "90")
	req.Header.Set("x-aws-log-class", "INFREQUENT_ACCESS")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	m.AssertCalled(t, "CreateLogGroup", "/test/my-group", types.LogGroupClassInfrequentAccess)
	<-done
	ext.retention.Stop()
	m.AssertCalled(t, "PutRetentionPolicy", "/test/my-group", int32(90))
}

func TestRoundTripper_RetentionOnExistingGroup(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/existing-group", "my-stream").Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/existing-group", int32(30)).Return(nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/existing-group")
	req.Header.Set("x-aws-log-stream", "my-stream")
	req.Header.Set("x-aws-log-retention-days", "30")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	m.AssertNotCalled(t, "CreateLogGroup", mock.Anything, mock.Anything)
	<-done
	ext.retention.Stop()
	m.AssertCalled(t, "PutRetentionPolicy", "/test/existing-group", int32(30))
}

func TestRoundTripper_InvalidRetention_Skipped(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "stream").Return(nil)

	ext := newTestExtension(t, &Config{}, m)
	ext.host = &mockHost{extensions: map[component.ID]component.Component{}}

	base := roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK}, nil
	})

	rt, err := ext.RoundTripper(base)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://logs.us-east-1.amazonaws.com/v1/logs", nil)
	req.Header.Set("x-aws-log-group", "/test/group")
	req.Header.Set("x-aws-log-stream", "stream")
	req.Header.Set("x-aws-log-retention-days", "42")

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)

	ext.retention.Stop()
	m.AssertNotCalled(t, "PutRetentionPolicy", mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "DescribeLogGroupsRetention", mock.Anything)
}

func TestProvision_StreamRetryOnNotFoundAfterOperationAborted(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	abortedErr := &types.OperationAbortedException{Message: aws.String("concurrent operation")}
	m := &mockCWLogsClient{}
	// CreateLogStream fails with NotFound (group doesn't exist yet).
	m.On("CreateLogStream", "/test/group", "stream-b").Return(notFoundErr).Once()
	// CreateLogGroup loses the race.
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(abortedErr)
	// First retry: still NotFound (winner's CreateLogGroup in flight).
	m.On("CreateLogStream", "/test/group", "stream-b").Return(notFoundErr).Once()
	// Second retry: succeeds (group now exists).
	m.On("CreateLogStream", "/test/group", "stream-b").Return(nil).Once()
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)
	result := ext.ensure(t.Context(), "/test/group", "stream-b", "")

	assert.True(t, result)
	m.AssertNumberOfCalls(t, "CreateLogStream", 3)
	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)
}

func TestProvision_StreamRetryExhaustedAfterOperationAborted(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	abortedErr := &types.OperationAbortedException{Message: aws.String("concurrent operation")}
	m := &mockCWLogsClient{}
	// All CreateLogStream calls return NotFound.
	m.On("CreateLogStream", "/test/group", "stream-c").Return(notFoundErr)
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(abortedErr)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, m)
	result := ext.ensure(t.Context(), "/test/group", "stream-c", "")

	assert.False(t, result)
	// 1 initial + maxStreamRetries post-group-create = 4 total
	m.AssertNumberOfCalls(t, "CreateLogStream", 1+maxStreamRetries)
}

func TestProvision_StreamNoRetryWhenGroupCreateSucceeds(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "stream-e").Return(notFoundErr)
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{
		LogsProvisionFailureBackoff: 60 * time.Second,
	}, m)
	result := ext.ensure(t.Context(), "/test/group", "stream-e", "")

	assert.False(t, result)
	// 1 initial + 1 post-group-create (no retries since group create succeeded)
	m.AssertNumberOfCalls(t, "CreateLogStream", 2)
}

func TestProvision_StreamRetryStopsOnNonNotFoundError(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	abortedErr := &types.OperationAbortedException{Message: aws.String("concurrent operation")}
	throttleErr := errors.New("throttled")
	m := &mockCWLogsClient{}
	m.On("CreateLogStream", "/test/group", "stream-d").Return(notFoundErr).Once()
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(abortedErr)
	// First retry: non-NotFound error, should stop immediately.
	m.On("CreateLogStream", "/test/group", "stream-d").Return(throttleErr).Once()
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)
	result := ext.ensure(t.Context(), "/test/group", "stream-d", "")

	assert.False(t, result)
	// 1 initial + 1 post-group-create (which hit throttle, no further retries)
	m.AssertNumberOfCalls(t, "CreateLogStream", 2)
}

func TestProvision_CreateLogGroupSingleflighted(t *testing.T) {
	notFoundErr := &types.ResourceNotFoundException{Message: aws.String("not found")}
	m := &mockCWLogsClient{}
	// All initial CreateLogStream calls fail with NotFound.
	m.On("CreateLogStream", "/test/group", mock.Anything).Return(notFoundErr).Once()
	m.On("CreateLogStream", "/test/group", mock.Anything).Return(notFoundErr).Once()
	m.On("CreateLogStream", "/test/group", mock.Anything).Return(notFoundErr).Once()
	// CreateLogGroup should only be called once despite 3 concurrent provisions.
	m.On("CreateLogGroup", "/test/group", types.LogGroupClass("")).Return(nil).Run(func(_ mock.Arguments) {
		time.Sleep(50 * time.Millisecond)
	})
	// Post-group retries succeed.
	m.On("CreateLogStream", "/test/group", mock.Anything).Return(nil)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{}, nil)

	ext := newTestExtension(t, &Config{}, m)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(stream string) {
			defer wg.Done()
			result := ext.ensure(t.Context(), "/test/group", stream, "")
			assert.True(t, result)
		}(fmt.Sprintf("stream-%d", i))
	}
	wg.Wait()

	m.AssertNumberOfCalls(t, "CreateLogGroup", 1)
}

func TestComponentLifecycle_Shutdown(t *testing.T) {
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "us-east-1", LocalMode: true},
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)
	require.NoError(t, ext.Shutdown(t.Context()))
}

func TestComponentLifecycle_StartShutdown(t *testing.T) {
	cfg := &Config{
		AWSSessionSettings: awsutilv2.AWSSessionSettings{Region: "us-east-1", LocalMode: true},
	}
	ext := newExtension(zaptest.NewLogger(t), cfg)
	require.NoError(t, ext.Start(t.Context(), componenttest.NewNopHost()))
	require.NoError(t, ext.Shutdown(t.Context()))
}
