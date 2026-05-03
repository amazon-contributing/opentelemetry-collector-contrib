// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package aggregator // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/signaltometricsconnector/internal/aggregator"

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// gaugeDP stores the last value for gauge metrics.
type gaugeDP struct {
	attrs pcommon.Map

	isDbl  bool
	intVal int64
	dblVal float64
}

func newGaugeDP(attrs pcommon.Map, isDbl bool) *gaugeDP {
	return &gaugeDP{
		isDbl: isDbl,
		attrs: attrs,
	}
}

func (dp *gaugeDP) SetInt(v int64) {
	dp.intVal = v
}

func (dp *gaugeDP) SetDouble(v float64) {
	dp.dblVal = v
}

func (dp *gaugeDP) Copy(
	timestamp time.Time,
	dest pmetric.NumberDataPoint,
) {
	dp.attrs.CopyTo(dest.Attributes())
	if dp.isDbl {
		dest.SetDoubleValue(dp.dblVal)
	} else {
		dest.SetIntValue(dp.intVal)
	}
	dest.SetTimestamp(pcommon.NewTimestampFromTime(timestamp))
}
