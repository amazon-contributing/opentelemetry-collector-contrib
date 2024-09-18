// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter

import (
	"errors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/cwlogs"
	"go.uber.org/zap"
	"time"
)

const (
	pusherMapLimit = 1000
)

type BoundedPusherMap struct {
	pusherMap          map[string]cwlogs.Pusher
	limit              int
	stalePusherTracker map[string]time.Time
}

func NewBoundedPusherMap() BoundedPusherMap {
	return BoundedPusherMap{
		pusherMap:          make(map[string]cwlogs.Pusher),
		limit:              pusherMapLimit,
		stalePusherTracker: make(map[string]time.Time),
	}
}

func (bpm *BoundedPusherMap) Add(key string, pusher cwlogs.Pusher, logger *zap.Logger) {
	err := bpm.EvictStalePushers()
	if err != nil {
		logger.Error("Error with evicting stale pushers", zap.Error(err))
		return
	}
	bpm.pusherMap[key] = pusher
	bpm.stalePusherTracker[key] = time.Now()
}

func (bpm *BoundedPusherMap) Get(key string) (cwlogs.Pusher, bool) {
	pusher, ok := bpm.pusherMap[key]
	if ok {
		bpm.stalePusherTracker[key] = time.Now()
	}
	return pusher, ok
}

func (bpm *BoundedPusherMap) EvictStalePushers() error {
	if len(bpm.pusherMap) < bpm.limit {
		return nil
	}
	now := time.Now()
	for key, lastUsed := range bpm.stalePusherTracker {
		if now.Sub(lastUsed) > time.Hour {
			delete(bpm.pusherMap, key)
			delete(bpm.stalePusherTracker, key)
		}
	}
	// Ideally, we should now be below the pusher limit. If we aren't, especially after deleting pushers older than an hour,
	// we should log an error.
	if len(bpm.pusherMap) >= bpm.limit {
		return errors.New("too many emf pushers being created. Dropping the request")
	}
	return nil
}

func (bpm *BoundedPusherMap) ListAllPushers() []cwlogs.Pusher {
	var pushers []cwlogs.Pusher
	for _, pusher := range bpm.pusherMap {
		pushers = append(pushers, pusher)
	}
	return pushers
}
