// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/cwlogs"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"testing"
	"time"
)

// MockPusher implements the cwlogs.Pusher interface for testing
type MockPusher struct{}

func (m MockPusher) AddLogEntry(_ *cwlogs.Event) error {
	return nil
}

func (m MockPusher) ForceFlush() error {
	return nil
}

func TestNewBoundedPusherMap(t *testing.T) {
	bpm := NewBoundedPusherMap()
	assert.Equal(t, pusherMapLimit, bpm.limit)
	assert.Empty(t, bpm.pusherMap)
	assert.Empty(t, bpm.stalePusherTracker)
}

func TestBoundedPusherMap_Add(t *testing.T) {
	bpm := NewBoundedPusherMap()
	logger := zap.NewNop()
	pusher := MockPusher{}

	bpm.Add("key1", pusher, logger)
	assert.Len(t, bpm.pusherMap, 1)
	assert.Len(t, bpm.stalePusherTracker, 1)
	assert.Contains(t, bpm.pusherMap, "key1")
	assert.Contains(t, bpm.stalePusherTracker, "key1")
}

func TestBoundedPusherMap_Get(t *testing.T) {
	bpm := NewBoundedPusherMap()
	pusher := MockPusher{}
	bpm.pusherMap["key1"] = pusher
	bpm.stalePusherTracker["key1"] = time.Now().Add(-30 * time.Minute)

	// Test getting an existing pusher
	gotPusher, ok := bpm.Get("key1")
	assert.True(t, ok)
	assert.Equal(t, pusher, gotPusher)
	assert.True(t, bpm.stalePusherTracker["key1"].After(time.Now().Add(-1*time.Second)))

	// Test getting a non-existent pusher
	gotPusher, ok = bpm.Get("key2")
	assert.False(t, ok)
	assert.Nil(t, gotPusher)
}

func TestBoundedPusherMap_EvictStalePushers(t *testing.T) {
	bpm := NewBoundedPusherMap()
	bpm.limit = 2
	pusher := MockPusher{}

	// Add two pushers, one stale and one fresh
	bpm.pusherMap["stale"] = pusher
	bpm.stalePusherTracker["stale"] = time.Now().Add(-2 * time.Hour)
	bpm.pusherMap["fresh"] = pusher
	bpm.stalePusherTracker["fresh"] = time.Now()

	err := bpm.EvictStalePushers()
	assert.NoError(t, err)
	assert.Len(t, bpm.pusherMap, 1)
	assert.Len(t, bpm.stalePusherTracker, 1)
	assert.NotContains(t, bpm.pusherMap, "stale")
	assert.NotContains(t, bpm.stalePusherTracker, "stale")
	assert.Contains(t, bpm.pusherMap, "fresh")
	assert.Contains(t, bpm.stalePusherTracker, "fresh")
}

func TestBoundedPusherMap_EvictStalePushers_Error(t *testing.T) {
	bpm := NewBoundedPusherMap()
	bpm.limit = 2
	pusher := MockPusher{}

	// Add two fresh pushers
	bpm.pusherMap["key1"] = pusher
	bpm.pusherMap["key2"] = pusher
	bpm.stalePusherTracker["key1"] = time.Now()
	bpm.stalePusherTracker["key2"] = time.Now()

	err := bpm.EvictStalePushers()
	assert.Error(t, err)
	assert.Equal(t, "too many emf pushers being created. Dropping the request", err.Error())
}

func TestBoundedPusherMap_ListAllPushers(t *testing.T) {
	bpm := NewBoundedPusherMap()
	pusher1 := MockPusher{}
	pusher2 := MockPusher{}
	bpm.Add("key1", pusher1, zap.NewExample())
	bpm.Add("key2", pusher2, zap.NewExample())

	pushers := bpm.ListAllPushers()
	assert.Len(t, pushers, 2)
	assert.Contains(t, pushers, pusher1)
	assert.Contains(t, pushers, pusher2)
}

func TestBoundedPusherMap_Add_EvictionError(t *testing.T) {
	bpm := NewBoundedPusherMap()
	bpm.limit = 1
	logger := zap.NewNop()
	pusher := MockPusher{}

	// Add one pusher to reach the limit
	bpm.Add("key1", pusher, logger)

	// Try to add another pusher, which should trigger eviction
	bpm.Add("key2", pusher, logger)

	// Check that the second pusher was not added due to eviction error
	assert.Len(t, bpm.pusherMap, 1)
	assert.Len(t, bpm.stalePusherTracker, 1)
	assert.Contains(t, bpm.pusherMap, "key1")
	assert.NotContains(t, bpm.pusherMap, "key2")
}
