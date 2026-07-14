// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/awscloudwatchlogsprovisionerextension"

import (
	"context"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	maxBatchSize        = 50
	batchInterval       = 5 * time.Second
	maxRetentionRetries = 3
	baseRetryDelay      = 1 * time.Second
	maxRetryDelay       = 10 * time.Second
)

// validRetentionDays is the set of allowed values for PutRetentionPolicy.
var validRetentionDays = map[int32]struct{}{
	1: {}, 3: {}, 5: {}, 7: {}, 14: {}, 30: {}, 60: {}, 90: {},
	120: {}, 150: {}, 180: {}, 365: {}, 400: {}, 545: {}, 731: {},
	1096: {}, 1827: {}, 2192: {}, 2557: {}, 2922: {}, 3288: {}, 3653: {},
}

func parseRetentionDays(s string) int32 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil || v <= 0 {
		return 0
	}
	if _, ok := validRetentionDays[int32(v)]; !ok {
		return 0
	}
	return int32(v)
}

type retentionRequest struct {
	logGroup        string
	retentionInDays int32
}

// retentionManager handles asynchronous PutRetentionPolicy calls with
// deduplication and retries. Requests accumulate in a pending map and a
// single background worker processes them in batches every batchInterval.
type retentionManager struct {
	logger         *zap.Logger
	client         cwLogsClient
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	batchInterval  time.Duration
	retryBaseDelay time.Duration
	failureBackoff time.Duration

	mu      sync.Mutex
	pending map[string]int32 // log group -> retention days
	// cache tracks processed groups. Values are time.Time (expiresAt). Zero
	// means success (permanent until evicted). Non-zero means failure (a new
	// Enqueue is accepted after that time).
	cache sync.Map
}

func newRetentionManager(logger *zap.Logger, failureBackoff time.Duration) *retentionManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &retentionManager{
		logger:         logger,
		ctx:            ctx,
		cancel:         cancel,
		batchInterval:  batchInterval,
		retryBaseDelay: baseRetryDelay,
		failureBackoff: failureBackoff,
		pending:        make(map[string]int32),
	}
}

// Start launches the background worker. Single-lifecycle, cannot be restarted.
func (rm *retentionManager) Start(client cwLogsClient) {
	rm.client = client
	rm.wg.Add(1)
	go rm.worker()
}

func (rm *retentionManager) Stop() {
	rm.cancel()
	rm.wg.Wait()
}

// Enqueue submits a retention request for asynchronous processing. A group
// is processed once, then ignored until evicted or failureBackoff expires.
// Re-enqueuing while a request is still pending updates the value (last
// write wins).
func (rm *retentionManager) Enqueue(logGroup string, retentionInDays int32) {
	if rm.isCached(logGroup) {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	// Double-check since takeBatch stores cache entries under mu.
	if rm.isCached(logGroup) {
		return
	}
	rm.pending[logGroup] = retentionInDays
}

// isCached reports whether the group has a non-expired cache entry.
func (rm *retentionManager) isCached(logGroup string) bool {
	v, ok := rm.cache.Load(logGroup)
	if !ok {
		return false
	}
	expiresAt := v.(time.Time)
	return expiresAt.IsZero() || time.Now().Before(expiresAt)
}

// Evict removes all state for a log group, allowing it to be re-enqueued.
func (rm *retentionManager) Evict(logGroup string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.cache.Delete(logGroup)
	delete(rm.pending, logGroup)
}

func (rm *retentionManager) worker() {
	defer rm.wg.Done()

	ticker := time.NewTicker(rm.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if batch := rm.takeBatch(); len(batch) > 0 {
				rm.processBatch(batch)
			}
		case <-rm.ctx.Done():
			return
		}
	}
}

// takeBatch drains up to maxBatchSize pending requests into a batch and
// caches them as successful. Overflow stays pending for the next tick.
func (rm *retentionManager) takeBatch() []retentionRequest {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	batch := make([]retentionRequest, 0, min(len(rm.pending), maxBatchSize))
	for logGroup, days := range rm.pending {
		if len(batch) == maxBatchSize {
			break
		}
		batch = append(batch, retentionRequest{logGroup: logGroup, retentionInDays: days})
		delete(rm.pending, logGroup)
		rm.cache.Store(logGroup, time.Time{})
	}
	return batch
}

func (rm *retentionManager) processBatch(batch []retentionRequest) {
	names := make([]string, len(batch))
	for i, req := range batch {
		names[i] = req.logGroup
	}

	var current map[string]int32
	if err := rm.withRetry(func() error {
		var err error
		current, err = rm.client.DescribeLogGroupsRetention(rm.ctx, names)
		return err
	}); err != nil {
		rm.logger.Warn("Failed to describe log groups retention after retries, will attempt to set all",
			zap.Error(err),
		)
	}

	for _, req := range batch {
		if rm.ctx.Err() != nil {
			return
		}
		if current != nil {
			if gotDays, ok := current[req.logGroup]; ok && gotDays == req.retentionInDays {
				rm.logger.Debug("Retention policy already matches, skipping",
					zap.String("logGroup", req.logGroup),
					zap.Int32("retentionInDays", req.retentionInDays),
				)
				continue
			}
		}
		if err := rm.withRetry(func() error {
			return rm.client.PutRetentionPolicy(rm.ctx, req.logGroup, req.retentionInDays)
		}); err != nil {
			rm.cache.Store(req.logGroup, time.Now().Add(rm.failureBackoff))
			rm.logger.Warn("Failed to set retention policy after retries",
				zap.String("logGroup", req.logGroup),
				zap.Int32("retentionInDays", req.retentionInDays),
				zap.Duration("retryBackoff", rm.failureBackoff),
				zap.Error(err),
			)
		}
	}
}

// withRetry calls op up to maxRetentionRetries times with jittered backoff.
func (rm *retentionManager) withRetry(op func() error) error {
	var err error
	for attempt := range maxRetentionRetries {
		if err = op(); err == nil {
			return nil
		}
		if attempt < maxRetentionRetries-1 {
			select {
			case <-rm.ctx.Done():
				return err
			case <-time.After(rm.backoffWithJitter(attempt)):
			}
		}
	}
	return err
}

// backoffWithJitter returns base * 2^attempt capped at maxRetryDelay, then
// applies uniform jitter in [delay/2, delay].
func (rm *retentionManager) backoffWithJitter(attempt int) time.Duration {
	delay := rm.retryBaseDelay * time.Duration(1<<uint(attempt))
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	half := int64(delay / 2)
	if half <= 0 {
		return delay
	}
	return time.Duration(half + rand.Int64N(half))
}
