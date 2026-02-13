// This file is manually maintained to match metadata.yaml.
// It follows the mdatagen conventions but is not auto-generated.

package metadata

import (
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
)

// --- helper to build a cumulative monotonic sum metric ---

type cumulativeSumMetric struct {
	data        pmetric.Metric
	config      MetricConfig
	capacity    int
	name        string
	description string
	unit        string
}

func (m *cumulativeSumMetric) init() {
	m.data = pmetric.NewMetric()
	m.data.SetName(m.name)
	m.data.SetDescription(m.description)
	m.data.SetUnit(m.unit)
	m.data.SetEmptySum()
	m.data.Sum().SetIsMonotonic(true)
	m.data.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
}

func (m *cumulativeSumMetric) recordDataPoint(start pcommon.Timestamp, ts pcommon.Timestamp, val int64) {
	if !m.config.Enabled {
		return
	}
	dp := m.data.Sum().DataPoints().AppendEmpty()
	dp.SetStartTimestamp(start)
	dp.SetTimestamp(ts)
	dp.SetIntValue(val)
}

func (m *cumulativeSumMetric) emit(metrics pmetric.MetricSlice) {
	if m.config.Enabled && m.data.Sum().DataPoints().Len() > 0 {
		if m.data.Sum().DataPoints().Len() > m.capacity {
			m.capacity = m.data.Sum().DataPoints().Len()
		}
		m.data.MoveTo(metrics.AppendEmpty())
		m.init()
	}
}

func newCumulativeSumMetric(cfg MetricConfig, name, description, unit string) cumulativeSumMetric {
	m := cumulativeSumMetric{config: cfg, name: name, description: description, unit: unit}
	if cfg.Enabled {
		m.init()
	}
	return m
}

// --- metric type aliases ---

type metricNodeEfaRdmaReadBytes struct{ cumulativeSumMetric }
type metricNodeEfaRdmaWriteBytes struct{ cumulativeSumMetric }
type metricNodeEfaRdmaWriteRecvBytes struct{ cumulativeSumMetric }
type metricNodeEfaRxBytes struct{ cumulativeSumMetric }
type metricNodeEfaRxDropped struct{ cumulativeSumMetric }
type metricNodeEfaTxBytes struct{ cumulativeSumMetric }
type metricNodeEfaRetransBytes struct{ cumulativeSumMetric }
type metricNodeEfaRetransPkts struct{ cumulativeSumMetric }
type metricNodeEfaRetransTimeoutEvents struct{ cumulativeSumMetric }
type metricNodeEfaUnresponsiveRemoteEvents struct{ cumulativeSumMetric }
type metricNodeEfaImpairedRemoteConnEvents struct{ cumulativeSumMetric }

// --- MetricsBuilder ---

type MetricsBuilder struct {
	config                                MetricsBuilderConfig
	startTime                             pcommon.Timestamp
	metricsCapacity                       int
	metricsBuffer                         pmetric.Metrics
	buildInfo                             component.BuildInfo
	metricNodeEfaRdmaReadBytes            metricNodeEfaRdmaReadBytes
	metricNodeEfaRdmaWriteBytes           metricNodeEfaRdmaWriteBytes
	metricNodeEfaRdmaWriteRecvBytes       metricNodeEfaRdmaWriteRecvBytes
	metricNodeEfaRxBytes                  metricNodeEfaRxBytes
	metricNodeEfaRxDropped                metricNodeEfaRxDropped
	metricNodeEfaTxBytes                  metricNodeEfaTxBytes
	metricNodeEfaRetransBytes             metricNodeEfaRetransBytes
	metricNodeEfaRetransPkts              metricNodeEfaRetransPkts
	metricNodeEfaRetransTimeoutEvents     metricNodeEfaRetransTimeoutEvents
	metricNodeEfaUnresponsiveRemoteEvents metricNodeEfaUnresponsiveRemoteEvents
	metricNodeEfaImpairedRemoteConnEvents metricNodeEfaImpairedRemoteConnEvents
}

type MetricBuilderOption interface{ apply(*MetricsBuilder) }
type metricBuilderOptionFunc func(mb *MetricsBuilder)

func (f metricBuilderOptionFunc) apply(mb *MetricsBuilder) { f(mb) }

func WithStartTime(startTime pcommon.Timestamp) MetricBuilderOption {
	return metricBuilderOptionFunc(func(mb *MetricsBuilder) { mb.startTime = startTime })
}

func NewMetricsBuilder(mbc MetricsBuilderConfig, settings receiver.Settings, options ...MetricBuilderOption) *MetricsBuilder {
	mb := &MetricsBuilder{
		config:        mbc,
		startTime:     pcommon.NewTimestampFromTime(time.Now()),
		metricsBuffer: pmetric.NewMetrics(),
		buildInfo:     settings.BuildInfo,
		metricNodeEfaRdmaReadBytes:            metricNodeEfaRdmaReadBytes{newCumulativeSumMetric(mbc.Metrics.NodeEfaRdmaReadBytes, "node_efa_rdma_read_bytes", "The number of bytes received using RDMA read operations", "By")},
		metricNodeEfaRdmaWriteBytes:           metricNodeEfaRdmaWriteBytes{newCumulativeSumMetric(mbc.Metrics.NodeEfaRdmaWriteBytes, "node_efa_rdma_write_bytes", "The number of bytes written by other instances using RDMA write operations", "By")},
		metricNodeEfaRdmaWriteRecvBytes:       metricNodeEfaRdmaWriteRecvBytes{newCumulativeSumMetric(mbc.Metrics.NodeEfaRdmaWriteRecvBytes, "node_efa_rdma_write_recv_bytes", "The number of bytes received by RDMA write operations", "By")},
		metricNodeEfaRxBytes:                  metricNodeEfaRxBytes{newCumulativeSumMetric(mbc.Metrics.NodeEfaRxBytes, "node_efa_rx_bytes", "The number of bytes received", "By")},
		metricNodeEfaRxDropped:                metricNodeEfaRxDropped{newCumulativeSumMetric(mbc.Metrics.NodeEfaRxDropped, "node_efa_rx_dropped", "The number of packets that were received and then dropped", "1")},
		metricNodeEfaTxBytes:                  metricNodeEfaTxBytes{newCumulativeSumMetric(mbc.Metrics.NodeEfaTxBytes, "node_efa_tx_bytes", "The number of bytes transmitted", "By")},
		metricNodeEfaRetransBytes:             metricNodeEfaRetransBytes{newCumulativeSumMetric(mbc.Metrics.NodeEfaRetransBytes, "node_efa_retrans_bytes", "The number of EFA SRD bytes retransmitted", "By")},
		metricNodeEfaRetransPkts:              metricNodeEfaRetransPkts{newCumulativeSumMetric(mbc.Metrics.NodeEfaRetransPkts, "node_efa_retrans_pkts", "The number of EFA SRD packets retransmitted", "1")},
		metricNodeEfaRetransTimeoutEvents:     metricNodeEfaRetransTimeoutEvents{newCumulativeSumMetric(mbc.Metrics.NodeEfaRetransTimeoutEvents, "node_efa_retrans_timeout_events", "The number of times EFA SRD traffic timed out and resulted in a network path change", "1")},
		metricNodeEfaUnresponsiveRemoteEvents: metricNodeEfaUnresponsiveRemoteEvents{newCumulativeSumMetric(mbc.Metrics.NodeEfaUnresponsiveRemoteEvents, "node_efa_unresponsive_remote_events", "The number of times an EFA SRD remote connection was unresponsive", "1")},
		metricNodeEfaImpairedRemoteConnEvents: metricNodeEfaImpairedRemoteConnEvents{newCumulativeSumMetric(mbc.Metrics.NodeEfaImpairedRemoteConnEvents, "node_efa_impaired_remote_conn_events", "The number of times EFA SRD connections entered an impaired state resulting in a reduced throughput rate limit", "1")},
	}
	for _, op := range options {
		op.apply(mb)
	}
	return mb
}

func (mb *MetricsBuilder) NewResourceBuilder() *ResourceBuilder {
	return NewResourceBuilder(mb.config.ResourceAttributes)
}

type ResourceMetricsOption interface{ apply(pmetric.ResourceMetrics) }
type resourceMetricsOptionFunc func(pmetric.ResourceMetrics)

func (f resourceMetricsOptionFunc) apply(rm pmetric.ResourceMetrics) { f(rm) }

func WithResource(res pcommon.Resource) ResourceMetricsOption {
	return resourceMetricsOptionFunc(func(rm pmetric.ResourceMetrics) { res.CopyTo(rm.Resource()) })
}

func (mb *MetricsBuilder) EmitForResource(options ...ResourceMetricsOption) {
	rm := pmetric.NewResourceMetrics()
	ils := rm.ScopeMetrics().AppendEmpty()
	ils.Scope().SetName(ScopeName)
	ils.Scope().SetVersion(mb.buildInfo.Version)

	mb.metricNodeEfaRdmaReadBytes.emit(ils.Metrics())
	mb.metricNodeEfaRdmaWriteBytes.emit(ils.Metrics())
	mb.metricNodeEfaRdmaWriteRecvBytes.emit(ils.Metrics())
	mb.metricNodeEfaRxBytes.emit(ils.Metrics())
	mb.metricNodeEfaRxDropped.emit(ils.Metrics())
	mb.metricNodeEfaTxBytes.emit(ils.Metrics())
	mb.metricNodeEfaRetransBytes.emit(ils.Metrics())
	mb.metricNodeEfaRetransPkts.emit(ils.Metrics())
	mb.metricNodeEfaRetransTimeoutEvents.emit(ils.Metrics())
	mb.metricNodeEfaUnresponsiveRemoteEvents.emit(ils.Metrics())
	mb.metricNodeEfaImpairedRemoteConnEvents.emit(ils.Metrics())

	for _, op := range options {
		op.apply(rm)
	}
	if ils.Metrics().Len() > 0 {
		if ils.Metrics().Len() > mb.metricsCapacity {
			mb.metricsCapacity = ils.Metrics().Len()
		}
		rm.MoveTo(mb.metricsBuffer.ResourceMetrics().AppendEmpty())
	}
}

func (mb *MetricsBuilder) Emit(_ ...ResourceMetricsOption) pmetric.Metrics {
	metrics := mb.metricsBuffer
	mb.metricsBuffer = pmetric.NewMetrics()
	return metrics
}

func (mb *MetricsBuilder) RecordNodeEfaRdmaReadBytesDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRdmaReadBytes.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRdmaWriteBytesDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRdmaWriteBytes.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRdmaWriteRecvBytesDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRdmaWriteRecvBytes.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRxBytesDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRxBytes.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRxDroppedDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRxDropped.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaTxBytesDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaTxBytes.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRetransBytesDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRetransBytes.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRetransPktsDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRetransPkts.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaRetransTimeoutEventsDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaRetransTimeoutEvents.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaUnresponsiveRemoteEventsDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaUnresponsiveRemoteEvents.recordDataPoint(mb.startTime, ts, val)
}

func (mb *MetricsBuilder) RecordNodeEfaImpairedRemoteConnEventsDataPoint(ts pcommon.Timestamp, val int64) {
	mb.metricNodeEfaImpairedRemoteConnEvents.recordDataPoint(mb.startTime, ts, val)
}
