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
	{"tx_pkts", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaTxPktsDataPoint(ts, v)
	}},
	{"rx_pkts", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRxPktsDataPoint(ts, v)
	}},
	{"send_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaSendBytesDataPoint(ts, v)
	}},
	{"recv_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRecvBytesDataPoint(ts, v)
	}},
	{"send_wrs", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaSendWrsDataPoint(ts, v)
	}},
	{"recv_wrs", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRecvWrsDataPoint(ts, v)
	}},
	{"rdma_write_wrs", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaWriteWrsDataPoint(ts, v)
	}},
	{"rdma_read_wrs", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaReadWrsDataPoint(ts, v)
	}},
	{"rdma_write_wr_err", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaWriteWrErrDataPoint(ts, v)
	}},
	{"rdma_read_wr_err", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaReadWrErrDataPoint(ts, v)
	}},
	{"rdma_read_resp_bytes", func(mb *metadata.MetricsBuilder, ts pcommon.Timestamp, v int64) {
		mb.RecordEfaRdmaReadRespBytesDataPoint(ts, v)
	}},
}

type efaScraper struct {
	logger      *zap.Logger
	mb          *metadata.MetricsBuilder
	reader      sysFsReader
	hostPath    string
	eniResolver eniResolver
	eniCache    map[string]string
}

func newScraper(cfg *Config, settings receiver.Settings) *efaScraper {
	return &efaScraper{
		logger:   settings.Logger,
		mb:       metadata.NewMetricsBuilder(cfg.MetricsBuilderConfig, settings),
		hostPath: cfg.HostPath,
		eniCache: make(map[string]string),
	}
}

func (s *efaScraper) start(_ context.Context, _ component.Host) error {
	s.reader = newSysFsReader(s.hostPath, s.logger)
	s.eniResolver = newIMDSENIResolver()
	s.logger.Info("Starting AWS EFA receiver", zap.String("host_path", s.hostPath))
	return nil
}

func (s *efaScraper) scrape(_ context.Context) (pmetric.Metrics, error) {
	exists, err := s.reader.EfaDataExists()
	if err != nil {
		return pmetric.NewMetrics(), fmt.Errorf("failed to check EFA data: %w", err)
	}
	if !exists {
		s.logger.Debug("No EFA devices found or insufficient permissions, skipping scrape")
		return pmetric.NewMetrics(), nil
	}

	devices, err := s.readAllDevices()
	if err != nil {
		return pmetric.NewMetrics(), fmt.Errorf("failed to read EFA devices: %w", err)
	}

	now := pcommon.NewTimestampFromTime(time.Now())

	for _, dev := range devices {
		rb := s.mb.NewResourceBuilder()
		rb.SetAwsEfaDevice(dev.name)
		rb.SetAwsEfaPort(dev.port)
		if dev.eniID != "" {
			rb.SetAwsEfaEniID(dev.eniID)
		}

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

		eniID := s.resolveENI(name)

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
				eniID:    eniID,
				counters: counters,
			})
		}
	}

	return devices, nil
}

// resolveENI resolves the ENI ID for a device via GID → MAC → IMDS lookup.
// Results are cached permanently (including failures as empty string) to avoid
// repeated IMDS calls on every scrape interval. This means if IMDS is
// temporarily unavailable at startup, the empty result is sticky until the
// receiver is restarted. This is acceptable because ENI-to-device mappings
// don't change at runtime.
func (s *efaScraper) resolveENI(deviceName string) string {
	if eniID, ok := s.eniCache[deviceName]; ok {
		return eniID
	}

	gid, err := s.reader.ReadGID(deviceName)
	if err != nil {
		s.logger.Warn("Failed to read GID for EFA device, emitting metrics without eni_id",
			zap.String("device", deviceName), zap.Error(err))
		s.eniCache[deviceName] = ""
		return ""
	}

	mac, err := ipv6LinkLocalToMAC(gid)
	if err != nil {
		s.logger.Warn("Failed to convert GID to MAC for EFA device, emitting metrics without eni_id",
			zap.String("device", deviceName), zap.String("gid", gid), zap.Error(err))
		s.eniCache[deviceName] = ""
		return ""
	}

	eniID, err := s.eniResolver.GetENIID(mac)
	if err != nil {
		s.logger.Warn("Failed to resolve ENI ID from IMDS, emitting metrics without eni_id",
			zap.String("device", deviceName), zap.String("mac", mac), zap.Error(err))
		s.eniCache[deviceName] = ""
		return ""
	}

	s.logger.Info("Resolved ENI ID for EFA device",
		zap.String("device", deviceName), zap.String("eni_id", eniID))
	s.eniCache[deviceName] = eniID
	return eniID
}

// readCounters reads all known EFA counters for a device-port combination.
// Counters that are missing or unavailable (e.g., on older EFA driver versions)
// are silently skipped. Only unexpected I/O errors are accumulated.
func (s *efaScraper) readCounters(deviceName string, port string) (map[string]uint64, error) {
	var errs error
	counters := make(map[string]uint64, len(efaCounters))

	for _, c := range efaCounters {
		value, err := s.reader.ReadCounter(deviceName, port, c.name)
		if err != nil {
			if errors.Is(err, errCounterNotAvailable) {
				continue
			}
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
