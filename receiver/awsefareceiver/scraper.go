// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsefareceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsefareceiver"

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsefareceiver/internal/metadata"
)

// efaCounter ties a sysfs hw_counter file name to the MetricsBuilder method
// that records it. Defined once so the two can never drift out of sync.
type efaCounter struct {
	name   string // file name under hw_counters/
	record func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, val int64)
}

var efaCounters = []efaCounter{
	{"rdma_read_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaReadBytesDataPoint(ts, v)
	}},
	{"rdma_write_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaWriteBytesDataPoint(ts, v)
	}},
	{"rdma_write_recv_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaWriteRecvBytesDataPoint(ts, v)
	}},
	{"rx_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRxBytesDataPoint(ts, v)
	}},
	{"rx_drops", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRxDroppedDataPoint(ts, v)
	}},
	{"tx_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaTxBytesDataPoint(ts, v)
	}},
	{"retrans_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRetransBytesDataPoint(ts, v)
	}},
	{"retrans_pkts", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRetransPktsDataPoint(ts, v)
	}},
	{"retrans_timeout_events", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRetransTimeoutEventsDataPoint(ts, v)
	}},
	{"unresponsive_remote_events", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaUnresponsiveRemoteEventsDataPoint(ts, v)
	}},
	{"impaired_remote_conn_events", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaImpairedRemoteConnEventsDataPoint(ts, v)
	}},
}

type efaScraper struct {
	logger   *zap.Logger
	mb       *metadata.MetricsBuilder
	reader   sysFsReader
	hostPath string
}

func newScraper(cfg *Config, settings receiver.Settings) *efaScraper {
	return &efaScraper{
		logger:   settings.Logger,
		mb:       metadata.NewMetricsBuilder(cfg.MetricsBuilderConfig, settings),
		hostPath: cfg.HostPath,
	}
}

func (s *efaScraper) start(_ context.Context, _ component.Host) error {
	s.reader = newSysFsReader(s.hostPath, s.logger)
	s.logger.Info("Starting AWS EFA receiver", zap.String("host_path", s.hostPath))
	return nil
}

func (s *efaScraper) scrape(_ context.Context) (pmetric.Metrics, error) {
	exists, err := s.reader.EfaDataExists()
	if err != nil {
		return pmetric.NewMetrics(), fmt.Errorf("failed to check EFA data: %w", err)
	}
	if !exists {
		s.logger.Debug("No EFA devices found, skipping scrape")
		return pmetric.NewMetrics(), nil
	}

	devices, err := s.readAllDevices()
	if err != nil {
		return pmetric.NewMetrics(), fmt.Errorf("failed to read EFA devices: %w", err)
	}

	now := pcommon.NewTimestampFromTime(time.Now())

	for _, dev := range devices {
		rb := s.mb.NewResourceBuilder()
		rb.SetDevice(dev.name)
		rb.SetPort(dev.port)

		s.recordMetrics(now, dev.counters)
		s.mb.EmitForResource(metadata.WithResource(rb.Emit()))
	}

	return s.mb.Emit(), nil
}

func (s *efaScraper) readAllDevices() ([]efaDevice, error) {
	deviceNames, err := s.reader.ListDevices()
	if err != nil {
		return nil, err
	}

	var devices []efaDevice
	for _, name := range deviceNames {
		ports, err := s.reader.ListPorts(name)
		if err != nil {
			s.logger.Warn("Failed to list ports for EFA device",
				zap.String("device", name), zap.Error(err))
			continue
		}

		for _, port := range ports {
			counters, err := s.readCounters(name, port)
			if err != nil {
				s.logger.Warn("Partial failure reading counters for EFA device port",
					zap.String("device", name), zap.String("port", port), zap.Error(err))
			}
			if len(counters) == 0 {
				continue
			}
			devices = append(devices, efaDevice{
				name:     name,
				port:     port,
				counters: counters,
			})
		}
	}

	return devices, nil
}

func (s *efaScraper) readCounters(deviceName string, port string) (map[string]uint64, error) {
	var errs error
	counters := make(map[string]uint64, len(efaCounters))

	for _, c := range efaCounters {
		value, err := s.reader.ReadCounter(deviceName, port, c.name)
		if err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		counters[c.name] = value
	}

	return counters, errs
}

func (s *efaScraper) recordMetrics(ts pcommon.Timestamp, counters map[string]uint64) {
	for _, c := range efaCounters {
		val, ok := counters[c.name]
		if !ok {
			continue
		}
		if val > math.MaxInt64 {
			s.logger.Warn("Skipping metric, value exceeds int64 range",
				zap.String("counter", c.name), zap.Uint64("value", val))
			continue
		}
		c.record(s.mb, ts, int64(val))
	}
}
