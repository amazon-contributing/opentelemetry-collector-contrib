// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsefareceiver

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awsefareceiver/internal/metadata"
)

type mockSysFsReader struct {
	exists     bool
	existsErr  error
	devices    []string
	devicesErr error
	ports      map[string][]string
	portsErr   map[string]error
	counters   map[string]map[string]map[string]uint64 // device -> port -> counter -> value
	counterErr map[string]map[string]map[string]error  // device -> port -> counter -> error
}

func (m *mockSysFsReader) EfaDataExists() (bool, error) {
	return m.exists, m.existsErr
}

func (m *mockSysFsReader) ListDevices() ([]string, error) {
	return m.devices, m.devicesErr
}

func (m *mockSysFsReader) ListPorts(deviceName string) ([]string, error) {
	if m.portsErr != nil {
		if err, ok := m.portsErr[deviceName]; ok {
			return nil, err
		}
	}
	return m.ports[deviceName], nil
}

func (m *mockSysFsReader) ReadCounter(deviceName string, port string, counter string) (uint64, error) {
	if m.counterErr != nil {
		if dev, ok := m.counterErr[deviceName]; ok {
			if p, ok := dev[port]; ok {
				if e, ok := p[counter]; ok {
					return 0, e
				}
			}
		}
	}
	if dev, ok := m.counters[deviceName]; ok {
		if p, ok := dev[port]; ok {
			if v, ok := p[counter]; ok {
				return v, nil
			}
		}
	}
	return 0, nil
}

// zeroCounters returns a counter map with all known counters set to 0.
func zeroCounters() map[string]uint64 {
	m := make(map[string]uint64, len(efaCounters))
	for _, c := range efaCounters {
		m[c.name] = 0
	}
	return m
}

// withValues returns a copy of base with the given overrides applied.
func withValues(base map[string]uint64, overrides map[string]uint64) map[string]uint64 {
	m := make(map[string]uint64, len(base))
	for k, v := range base {
		m[k] = v
	}
	for k, v := range overrides {
		m[k] = v
	}
	return m
}

func newTestMock() *mockSysFsReader {
	return &mockSysFsReader{
		exists:  true,
		devices: []string{"rdmap0s31", "rdmap1s31"},
		ports: map[string][]string{
			"rdmap0s31": {"1"},
			"rdmap1s31": {"1"},
		},
		counters: map[string]map[string]map[string]uint64{
			"rdmap0s31": {"1": withValues(zeroCounters(), map[string]uint64{
				"rdma_read_bytes":             1000,
				"rdma_write_bytes":            2000,
				"rdma_write_recv_bytes":       3000,
				"rx_bytes":                    4000,
				"rx_drops":                    5,
				"tx_bytes":                    5000,
				"retrans_bytes":               100,
				"retrans_pkts":                10,
				"retrans_timeout_events":      1,
				"unresponsive_remote_events":  2,
				"impaired_remote_conn_events": 3,
			})},
			"rdmap1s31": {"1": withValues(zeroCounters(), map[string]uint64{
				"rdma_read_bytes":       6000,
				"rdma_write_bytes":      7000,
				"rdma_write_recv_bytes": 8000,
				"rx_bytes":              9000,
				"tx_bytes":              10000,
			})},
		},
	}
}

func TestScrape(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = newTestMock()

	metrics, err := s.scrape(t.Context())
	require.NoError(t, err)

	assert.Equal(t, 2, metrics.ResourceMetrics().Len())

	totalMetrics := 0
	for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
		rm := metrics.ResourceMetrics().At(i)
		attrs := rm.Resource().Attributes()

		device, ok := attrs.Get("device")
		assert.True(t, ok)
		assert.Contains(t, []string{"rdmap0s31", "rdmap1s31"}, device.Str())

		port, ok := attrs.Get("port")
		assert.True(t, ok)
		assert.Equal(t, "1", port.Str())

		// Device-level metrics always carry pod/namespace/container labels (empty when unassigned)
		pod, ok := attrs.Get("pod")
		assert.True(t, ok)
		assert.Equal(t, "", pod.Str())

		ns, ok := attrs.Get("namespace")
		assert.True(t, ok)
		assert.Equal(t, "", ns.Str())

		container, ok := attrs.Get("container")
		assert.True(t, ok)
		assert.Equal(t, "", container.Str())

		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			totalMetrics += rm.ScopeMetrics().At(j).Metrics().Len()
		}
	}

	// 11 metrics per device * 2 devices = 22
	assert.Equal(t, 22, totalMetrics)
}

func TestScrapeNoEfaDevices(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = &mockSysFsReader{exists: false}

	metrics, err := s.scrape(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, metrics.ResourceMetrics().Len())
}

func TestStart(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)

	err := s.start(t.Context(), componenttest.NewNopHost())
	require.NoError(t, err)
	assert.NotNil(t, s.reader)
}

func TestScrapeMetricValues(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = &mockSysFsReader{
		exists:  true,
		devices: []string{"rdmap0s31"},
		ports:   map[string][]string{"rdmap0s31": {"1"}},
		counters: map[string]map[string]map[string]uint64{
			"rdmap0s31": {"1": withValues(zeroCounters(), map[string]uint64{
				"rdma_read_bytes": 42,
			})},
		},
	}

	metrics, err := s.scrape(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, metrics.ResourceMetrics().Len())

	sm := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0)
	found := false
	for i := 0; i < sm.Metrics().Len(); i++ {
		m := sm.Metrics().At(i)
		if m.Name() == "efa_rdma_read_bytes" {
			assert.Equal(t, int64(42), m.Sum().DataPoints().At(0).IntValue())
			found = true
		}
	}
	assert.True(t, found, "expected to find efa_rdma_read_bytes metric")
}

func TestScrapeEfaDataExistsError(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = &mockSysFsReader{exists: false, existsErr: errors.New("permission denied")}

	metrics, err := s.scrape(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check EFA data")
	assert.Equal(t, 0, metrics.ResourceMetrics().Len())
}

func TestScrapePartialDeviceFailure(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = &mockSysFsReader{
		exists:  true,
		devices: []string{"efa0", "efa1"},
		ports:   map[string][]string{"efa0": {"1"}},
		portsErr: map[string]error{
			"efa1": errors.New("device removed"),
		},
		counters: map[string]map[string]map[string]uint64{
			"efa0": {"1": withValues(zeroCounters(), map[string]uint64{"rdma_read_bytes": 100})},
		},
	}

	metrics, err := s.scrape(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())
}

func TestScrapeCounterReadError(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = &mockSysFsReader{
		exists:  true,
		devices: []string{"efa0", "efa1"},
		ports: map[string][]string{
			"efa0": {"1"},
			"efa1": {"1"},
		},
		counters: map[string]map[string]map[string]uint64{
			"efa0": {"1": zeroCounters()},
			"efa1": {"1": withValues(zeroCounters(), map[string]uint64{"rdma_read_bytes": 100})},
		},
		counterErr: map[string]map[string]map[string]error{
			"efa0": {"1": {"rx_bytes": errors.New("I/O error")}},
		},
	}

	metrics, err := s.scrape(t.Context())
	require.NoError(t, err)
	// efa0 is included with partial counters (rx_bytes failed but others succeeded)
	assert.Equal(t, 2, metrics.ResourceMetrics().Len())
}

func TestRecordOverflow(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	settings := receivertest.NewNopSettings(metadata.Type)
	s := newScraper(cfg, settings)
	s.reader = &mockSysFsReader{
		exists:  true,
		devices: []string{"efa0"},
		ports:   map[string][]string{"efa0": {"1"}},
		counters: map[string]map[string]map[string]uint64{
			"efa0": {"1": withValues(zeroCounters(), map[string]uint64{
				"rdma_read_bytes": math.MaxUint64,
				"tx_bytes":        200,
			})},
		},
	}

	metrics, err := s.scrape(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, metrics.ResourceMetrics().Len())

	sm := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0)

	// rdma_read_bytes overflows int64, so it's skipped: 10 instead of 11
	assert.Equal(t, 10, sm.Metrics().Len())

	for i := 0; i < sm.Metrics().Len(); i++ {
		m := sm.Metrics().At(i)
		if m.Name() == "efa_tx_bytes" {
			assert.Equal(t, int64(200), m.Sum().DataPoints().At(0).IntValue())
		}
		assert.NotEqual(t, "efa_rdma_read_bytes", m.Name())
	}
}
