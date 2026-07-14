// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap/zaptest"
)

// newTestRetentionManager returns a retention manager with short intervals.
// The worker is not started — tests enqueue first, then call rm.Start(client)
// so that the first batch deterministically contains everything enqueued.
func newTestRetentionManager(t *testing.T) *retentionManager {
	rm := newRetentionManager(zaptest.NewLogger(t), time.Minute)
	rm.batchInterval = 10 * time.Millisecond
	rm.retryBaseDelay = time.Millisecond
	t.Cleanup(rm.Stop)
	return rm
}

func TestEnsureRetention_Success(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 0}, nil)
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-done
	rm.Stop()

	m.AssertCalled(t, "PutRetentionPolicy", "/test/group", int32(90))
	m.AssertNumberOfCalls(t, "PutRetentionPolicy", 1)
}

func TestEnsureRetention_Dedup(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 0}, nil)
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(30)).Return(nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Enqueue("/test/group", 90)
	rm.Enqueue("/test/group", 30)
	rm.Start(m)
	<-done

	// Once processed, further enqueues for the group are ignored.
	rm.Enqueue("/test/group", 90)
	rm.Stop()

	m.AssertNumberOfCalls(t, "PutRetentionPolicy", 1)
}

func TestEnsureRetention_DifferentGroups(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{
		"/test/group-a": 0,
		"/test/group-b": 0,
	}, nil)
	var putCount atomic.Int32
	done := make(chan struct{})
	m.On("PutRetentionPolicy", mock.Anything, mock.Anything).Return(nil).Run(func(_ mock.Arguments) {
		if putCount.Add(1) == 2 {
			close(done)
		}
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group-a", 90)
	rm.Enqueue("/test/group-b", 30)
	rm.Start(m)
	<-done
	rm.Stop()

	m.AssertNumberOfCalls(t, "PutRetentionPolicy", 2)
}

func TestEnsureRetention_RetriesOnFailure(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 0}, nil)
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(errors.New("throttled")).Run(func(_ mock.Arguments) {
		if len(filterCalls(m.Calls, "PutRetentionPolicy")) >= maxRetentionRetries {
			close(done)
		}
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-done
	rm.Stop()

	m.AssertNumberOfCalls(t, "PutRetentionPolicy", maxRetentionRetries)
}

func TestEnsureRetention_SucceedsAfterRetry(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 0}, nil)
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(errors.New("throttled")).Times(2)
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-done
	rm.Stop()

	m.AssertNumberOfCalls(t, "PutRetentionPolicy", 3)
}

func TestEnsureRetention_SkipsWhenAlreadyMatches(t *testing.T) {
	m := &mockCWLogsClient{}
	done := make(chan struct{})
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 90}, nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-done
	rm.Stop()

	m.AssertNotCalled(t, "PutRetentionPolicy", mock.Anything, mock.Anything)
}

func TestEnsureRetention_ProceedsWhenDescribeExhausted(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(nil, errors.New("access denied"))
	done := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-done
	rm.Stop()

	m.AssertCalled(t, "PutRetentionPolicy", "/test/group", int32(90))
}

func TestEnsureRetention_BatchesMultipleGroups(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{
		"/test/group-a": 0,
		"/test/group-b": 30, // already matches
		"/test/group-c": 0,
	}, nil)
	var putCount atomic.Int32
	done := make(chan struct{})
	m.On("PutRetentionPolicy", mock.Anything, mock.Anything).Return(nil).Run(func(_ mock.Arguments) {
		if putCount.Add(1) == 2 {
			close(done)
		}
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group-a", 90)
	rm.Enqueue("/test/group-b", 30) // should skip
	rm.Enqueue("/test/group-c", 365)
	rm.Start(m)
	<-done
	rm.Stop()

	// Only 1 DescribeLogGroupsRetention call for the batch
	m.AssertNumberOfCalls(t, "DescribeLogGroupsRetention", 1)
	// Only group-a and group-c need PutRetentionPolicy
	m.AssertNumberOfCalls(t, "PutRetentionPolicy", 2)
	m.AssertCalled(t, "PutRetentionPolicy", "/test/group-a", int32(90))
	m.AssertCalled(t, "PutRetentionPolicy", "/test/group-c", int32(365))
	m.AssertNotCalled(t, "PutRetentionPolicy", "/test/group-b", int32(30))
}

func TestEnsureRetention_DescribeRetriesThenSucceeds(t *testing.T) {
	m := &mockCWLogsClient{}
	done := make(chan struct{})
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(nil, errors.New("throttled")).Times(2)
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 90}, nil).Run(func(_ mock.Arguments) {
		close(done)
	})

	rm := newTestRetentionManager(t)
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-done
	rm.Stop()

	// Describe retried 3 times, found match, no Put needed
	m.AssertNumberOfCalls(t, "DescribeLogGroupsRetention", 3)
	m.AssertNotCalled(t, "PutRetentionPolicy", mock.Anything, mock.Anything)
}

func TestEnsureRetention_ReenqueueAfterFailureBackoff(t *testing.T) {
	m := &mockCWLogsClient{}
	m.On("DescribeLogGroupsRetention", mock.Anything).Return(map[string]int32{"/test/group": 0}, nil)
	putFailed := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(errors.New("access denied")).Times(maxRetentionRetries).Run(func(_ mock.Arguments) {
		if len(filterCalls(m.Calls, "PutRetentionPolicy")) == maxRetentionRetries {
			close(putFailed)
		}
	})
	putSucceeded := make(chan struct{})
	m.On("PutRetentionPolicy", "/test/group", int32(90)).Return(nil).Run(func(_ mock.Arguments) {
		close(putSucceeded)
	})

	rm := newTestRetentionManager(t)
	rm.failureBackoff = 20 * time.Millisecond
	rm.Enqueue("/test/group", 90)
	rm.Start(m)
	<-putFailed

	// Within backoff: rejected. (Poll-free check via the dedup predicate.)
	assert.True(t, rm.isCached("/test/group"))

	// After backoff expires, a re-enqueue is accepted and processed.
	assert.Eventually(t, func() bool {
		rm.Enqueue("/test/group", 90)
		select {
		case <-putSucceeded:
			return true
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
	rm.Stop()

	m.AssertNumberOfCalls(t, "PutRetentionPolicy", maxRetentionRetries+1)
}

func TestParseRetentionDays_Validation(t *testing.T) {
	tests := []struct {
		input    string
		expected int32
	}{
		{"", 0},
		{"90", 90},
		{"365", 365},
		{"30", 30},
		{"-1", 0}, // negative → skip
		{"0", 0},
		{"42", 0},       // not in valid set
		{"abc", 0},      // non-numeric
		{"99999999", 0}, // not in valid set
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, parseRetentionDays(tt.input), "input: %q", tt.input)
	}
}
