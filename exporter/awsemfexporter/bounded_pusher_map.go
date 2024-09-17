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

type boundedPusherMap struct {
	pusherMap          map[string]cwlogs.Pusher
	limit              int
	stalePusherTracker map[string]time.Time
}

func newBoundedPusherMap() boundedPusherMap {
	return boundedPusherMap{
		pusherMap:          make(map[string]cwlogs.Pusher),
		limit:              pusherMapLimit,
		stalePusherTracker: make(map[string]time.Time),
	}
}

func (bpm *boundedPusherMap) add(key string, pusher cwlogs.Pusher, logger *zap.Logger) {
	err := bpm.evictStalePushers()
	if err != nil {
		logger.Error("Error with evicting stale pushers", zap.Error(err))
		return
	}
	bpm.pusherMap[key] = pusher
	bpm.stalePusherTracker[key] = time.Now()
}

func (bpm *boundedPusherMap) get(key string) (cwlogs.Pusher, bool) {
	pusher, ok := bpm.pusherMap[key]
	if ok {
		bpm.stalePusherTracker[key] = time.Now()
	}
	return pusher, ok
}

func (bpm *boundedPusherMap) evictStalePushers() error {
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

func (bpm *boundedPusherMap) listAllPushers() []cwlogs.Pusher {
	var pushers []cwlogs.Pusher
	for _, pusher := range bpm.pusherMap {
		pushers = append(pushers, pusher)
	}
	return pushers
}
