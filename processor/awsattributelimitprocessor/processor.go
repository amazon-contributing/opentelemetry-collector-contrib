// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// phase1Prefixes contains prefix patterns for unconditional removal.
// All resource attributes matching any of these prefixes are always removed.
var phase1Prefixes = []string{
	"k8s.node.label.feature.node.kubernetes.io/",
	"k8s.node.label.beta.kubernetes.io/",
	"k8s.node.label.failure-domain.beta.kubernetes.io/",
	"k8s.node.label.alpha.eksctl.io/",
}

// phase1ExactKeys contains exact resource attribute keys for unconditional removal.
// These are redundant with existing OTel semantic convention resource attributes
// or have zero monitoring value.
var phase1ExactKeys = map[string]struct{}{
	"k8s.node.label.topology.kubernetes.io/region":                 {},
	"k8s.node.label.topology.kubernetes.io/zone":                   {},
	"k8s.node.label.topology.ebs.csi.aws.com/zone":                 {},
	"k8s.node.label.node.kubernetes.io/instance-type":              {},
	"k8s.node.label.kubernetes.io/hostname":                        {},
	"k8s.node.label.eks.amazonaws.com/nodegroup-image":             {},
	"k8s.node.label.k8s.io/cloud-provider-aws":                     {},
	"k8s.node.label.eks.amazonaws.com/sourceLaunchTemplateId":      {},
	"k8s.node.label.eks.amazonaws.com/sourceLaunchTemplateVersion": {},
	"k8s.pod.label.pod-template-hash":                              {},
	"k8s.pod.label.controller-revision-hash":                       {},
}

// attributeLimitProcessor enforces the Zeus attribute limit by removing
// redundant attributes (Phase 1) and dropping low-priority attributes
// by tier when the total count exceeds the configured maximum (Phase 2).
type attributeLimitProcessor struct {
	config       *Config
	logger       *zap.Logger
	mu           sync.Mutex
	lastLogAt    map[string]time.Time // rate-limiting: last log time per metric name
	lastEviction time.Time
}

func newProcessor(cfg *Config, logger *zap.Logger) *attributeLimitProcessor {
	return &attributeLimitProcessor{
		config:       cfg,
		logger:       logger,
		lastLogAt:    make(map[string]time.Time),
		lastEviction: time.Now(),
	}
}

// Start is a no-op for this processor (no external resources to initialize).
func (p *attributeLimitProcessor) Start(_ context.Context, _ component.Host) error {
	return nil
}

// Shutdown is a no-op for this processor (no external resources to release).
func (p *attributeLimitProcessor) Shutdown(_ context.Context) error {
	return nil
}

// removePhase1Attributes removes all resource attributes that match Phase 1
// unconditional removal rules (prefix patterns and exact keys) in a single pass.
func removePhase1Attributes(attrs pcommon.Map) {
	attrs.RemoveIf(func(key string, _ pcommon.Value) bool {
		// Check exact key match first (O(1) map lookup).
		if _, ok := phase1ExactKeys[key]; ok {
			return true
		}
		// Check prefix patterns.
		for _, prefix := range phase1Prefixes {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return false
	})
}

// attrEntry represents a droppable attribute with its tier and location.
type attrEntry struct {
	key         string
	tier        int
	isDatapoint bool
}

// attrEntryPool reduces allocations when Phase 2 runs frequently.
// We store *[]attrEntry (pointer to slice) rather than []attrEntry because
// sync.Pool stores interface{} values — storing the slice directly would cause
// the slice header to be boxed into an interface on every Get/Put, defeating
// the purpose. Storing a pointer avoids this allocation.
var attrEntryPool = sync.Pool{
	New: func() any {
		s := make([]attrEntry, 0, 64)
		return &s
	},
}

// removeExcessByTier collects droppable attributes, sorts by tier then alphabetically,
// and removes them until the excess count is satisfied.
// Returns the number of attributes dropped and the min/max tier used.
func removeExcessByTier(resourceAttrs pcommon.Map, datapointAttrs pcommon.Map, excess int) (droppedCount int, minTier int, maxTier int) {
	droppablePtr := attrEntryPool.Get().(*[]attrEntry)
	droppable := (*droppablePtr)[:0] // reset length, keep capacity
	defer func() {
		*droppablePtr = droppable[:0]
		attrEntryPool.Put(droppablePtr)
	}()

	// Scan resource attributes for tiers 1-7.
	resourceAttrs.Range(func(key string, _ pcommon.Value) bool {
		tier := classifyAttribute(key, false)
		if tier > 0 {
			droppable = append(droppable, attrEntry{key: key, tier: tier, isDatapoint: false})
		}
		return true
	})

	// Scan datapoint attributes for tier 8.
	datapointAttrs.Range(func(key string, _ pcommon.Value) bool {
		tier := classifyAttribute(key, true)
		if tier > 0 {
			droppable = append(droppable, attrEntry{key: key, tier: tier, isDatapoint: true})
		}
		return true
	})

	// Sort by tier ascending, then alphabetically within tier.
	slices.SortFunc(droppable, func(a, b attrEntry) int {
		if a.tier != b.tier {
			return a.tier - b.tier
		}
		if a.key < b.key {
			return -1
		}
		if a.key > b.key {
			return 1
		}
		return 0
	})

	// Drop until excess is satisfied.
	dropped := 0
	minT, maxT := 0, 0
	for _, entry := range droppable {
		if dropped >= excess {
			break
		}
		if entry.isDatapoint {
			datapointAttrs.Remove(entry.key)
		} else {
			resourceAttrs.Remove(entry.key)
		}
		dropped++
		if minT == 0 {
			minT = entry.tier
		}
		maxT = entry.tier
	}

	return dropped, minT, maxT
}

// logDropWarning emits a rate-limited warning when Phase 2 drops attributes.
// Logs at most once per metric name per minute.
func (p *attributeLimitProcessor) logDropWarning(metricName string, droppedCount int, minTier int, maxTier int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	// Evict stale entries every 5 minutes (not on every call).
	if now.Sub(p.lastEviction) > 5*time.Minute {
		for name, lastTime := range p.lastLogAt {
			if now.Sub(lastTime) > 5*time.Minute {
				delete(p.lastLogAt, name)
			}
		}
		p.lastEviction = now
	}
	// Rate limit: once per metric name per minute.
	if lastTime, ok := p.lastLogAt[metricName]; ok && now.Sub(lastTime) < time.Minute {
		return
	}
	p.lastLogAt[metricName] = now
	p.logger.Warn("dropped attributes to meet limit",
		zap.String("metric", metricName),
		zap.Int("dropped", droppedCount),
		zap.Int("minTier", minTier),
		zap.Int("maxTier", maxTier),
		zap.Int("limit", p.config.MaxTotalAttributes),
	)
}

// logExhaustedError logs a rate-limited error when all tiers are exhausted
// and the count still exceeds the limit.
func (p *attributeLimitProcessor) logExhaustedError(metricName string, remaining int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	errorKey := metricName + ":exhausted"
	if lastTime, ok := p.lastLogAt[errorKey]; ok && now.Sub(lastTime) < time.Minute {
		return
	}
	p.lastLogAt[errorKey] = now
	p.logger.Error("all tiers exhausted, still over limit",
		zap.String("metric", metricName),
		zap.Int("remaining", remaining),
		zap.Int("limit", p.config.MaxTotalAttributes),
	)
}

// enforceLimit checks if the total attribute count exceeds the limit and runs
// Phase 2 tier-based dropping if needed.
func (p *attributeLimitProcessor) enforceLimit(resourceAttrs pcommon.Map, datapointAttrs pcommon.Map, scopeAttrCount int, metricName string) {
	total := resourceAttrs.Len() + scopeAttrCount + datapointAttrs.Len()
	if total <= p.config.MaxTotalAttributes {
		return
	}

	excess := total - p.config.MaxTotalAttributes
	droppedCount, minTier, maxTier := removeExcessByTier(resourceAttrs, datapointAttrs, excess)

	if droppedCount > 0 {
		p.logDropWarning(metricName, droppedCount, minTier, maxTier)
	}

	// Check if still over limit after dropping all droppable attrs.
	remaining := resourceAttrs.Len() + scopeAttrCount + datapointAttrs.Len()
	if remaining > p.config.MaxTotalAttributes {
		p.logExhaustedError(metricName, remaining)
	}
}

// processDatapoints is a generic helper that enforces the attribute limit on
// all datapoints in a slice. It works with any datapoint type that exposes
// Attributes() pcommon.Map.
func processDatapoints[DP interface{ Attributes() pcommon.Map }](
	datapoints interface {
		Len() int
		At(int) DP
	},
	p *attributeLimitProcessor,
	resourceAttrs pcommon.Map,
	scopeAttrCount int,
	metricName string,
) {
	for i := 0; i < datapoints.Len(); i++ {
		dp := datapoints.At(i)
		p.enforceLimit(resourceAttrs, dp.Attributes(), scopeAttrCount, metricName)
	}
}

func (p *attributeLimitProcessor) processMetrics(_ context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		resourceAttrs := rm.Resource().Attributes()

		// Phase 1: Unconditional removal (always runs).
		removePhase1Attributes(resourceAttrs)

		// Phase 2: Per-datapoint evaluation.
		// Note: Resource attributes are shared across all datapoints in a ResourceMetrics.
		// If an earlier datapoint triggers Phase 2 and drops resource attributes, subsequent
		// datapoints see the already-trimmed resource attributes. This is by design — if any
		// datapoint is over the limit, the shared resource attributes need trimming regardless.
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			sm := rm.ScopeMetrics().At(j)
			scopeAttrCount := sm.Scope().Attributes().Len()

			for k := 0; k < sm.Metrics().Len(); k++ {
				m := sm.Metrics().At(k)
				metricName := m.Name()

				switch m.Type() {
				case pmetric.MetricTypeGauge:
					processDatapoints(m.Gauge().DataPoints(), p, resourceAttrs, scopeAttrCount, metricName)
				case pmetric.MetricTypeSum:
					processDatapoints(m.Sum().DataPoints(), p, resourceAttrs, scopeAttrCount, metricName)
				case pmetric.MetricTypeHistogram:
					processDatapoints(m.Histogram().DataPoints(), p, resourceAttrs, scopeAttrCount, metricName)
				case pmetric.MetricTypeExponentialHistogram:
					processDatapoints(m.ExponentialHistogram().DataPoints(), p, resourceAttrs, scopeAttrCount, metricName)
				case pmetric.MetricTypeSummary:
					processDatapoints(m.Summary().DataPoints(), p, resourceAttrs, scopeAttrCount, metricName)
				default:
					// Skip metrics with unsupported or empty type without error.
				}
			}
		}
	}
	return md, nil
}
