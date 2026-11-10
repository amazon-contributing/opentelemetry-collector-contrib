// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsattributelimitprocessor

import (
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

// BenchmarkProcessMetrics_UnderLimit measures the metrics hot path under the
// limit: the leaf must not be boxed into an interface per datapoint.
func BenchmarkProcessMetrics_UnderLimit(b *testing.B) {
	const datapoints = 1000
	const attrs = 10

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "svc")
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("bench_metric")
	g := m.SetEmptyGauge()
	for range datapoints {
		dp := g.DataPoints().AppendEmpty()
		dp.SetIntValue(1)
		dp.SetTimestamp(pcommon.NewTimestampFromTime(time.Now()))
		for a := range attrs {
			dp.Attributes().PutStr(fmt.Sprintf("attr%02d", a), "v")
		}
	}
	p := newProcessor(&Config{MaxTotalAttributes: maxAllowedAttributes}, zap.NewNop())

	// Under the limit, processMetrics does not mutate the data (enforceLimit
	// early-returns and no unconditional-removal keys are configured), so the
	// same md can be reused across iterations.
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.processMetrics(b.Context(), md); err != nil {
			b.Fatal(err)
		}
	}
}
