// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package targetallocator

import (
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/promslog"
	promconfig "github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/scrape"
	"github.com/prometheus/prometheus/util/teststorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/sharedpromconfig"
)

func TestNewManager(t *testing.T) {
	cfg := &Config{
		Interval:    30 * time.Second,
		CollectorID: "test-collector",
	}
	promCfg := sharedpromconfig.NewConfig(&promconfig.Config{
		ScrapeConfigs: []*promconfig.ScrapeConfig{
			{JobName: "test-job"},
		},
	})

	manager := NewManager(receivertest.NewNopSettings(metadata.Type), cfg, promCfg)

	assert.NotNil(t, manager)
	assert.Equal(t, cfg, manager.cfg)
	assert.Equal(t, promCfg, manager.promCfg)
	assert.NotNil(t, manager.shutdown)
	assert.NotNil(t, manager.configUpdateCount)
	assert.Len(t, manager.initialScrapeConfigs, 1)
}

func TestManagerShutdown(t *testing.T) {
	// Create a mock target allocator server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/scrape_configs" {
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer server.Close()

	cfg := &Config{
		Interval:    100 * time.Millisecond,
		CollectorID: "test-collector",
	}
	cfg.ClientConfig.Endpoint = server.URL
	promCfg, err := promconfig.Load("", nil)
	require.NoError(t, err)

	// Create a logger with observer to capture logs
	core, logs := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	settings := receivertest.NewNopSettings(metadata.Type)
	settings.Logger = logger

	manager := NewManager(settings, cfg, sharedpromconfig.NewConfig(promCfg))

	// Start the manager so the goroutine is running
	ctx := t.Context()

	// Initialize Prometheus managers using the same pattern as manager_test.go
	promLogger := promslog.NewNopLogger()
	reg := prometheus.NewRegistry()
	sdMetrics, err := discovery.CreateAndRegisterSDMetrics(reg)
	require.NoError(t, err)
	discoveryManager := discovery.NewManager(ctx, promLogger, reg, sdMetrics)
	require.NotNil(t, discoveryManager)

	store := teststorage.New(t)
	scrapeManager, err := scrape.NewManager(&scrape.Options{}, promLogger, nil, nil, store, reg)
	require.NoError(t, err)
	defer scrapeManager.Stop()

	err = manager.Start(ctx, componenttest.NewNopHost(), scrapeManager, discoveryManager)
	require.NoError(t, err)

	// Shutdown the manager. This blocks until the goroutine exits,
	// which logs "Stopping target allocator" before returning.
	manager.Shutdown()

	logEntries := logs.FilterMessage("Stopping target allocator")
	require.Positive(t, logEntries.Len(), "expected shutdown log message")
}

func TestInstantiateShard(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		envVar   string
		expected []byte
		setEnv   bool
	}{
		{
			name:     "shard environment variable set",
			input:    []byte("replica-$(SHARD)"),
			envVar:   "5",
			expected: []byte("replica-5"),
			setEnv:   true,
		},
		{
			name:     "shard environment variable not set",
			input:    []byte("replica-$(SHARD)"),
			expected: []byte("replica-0"),
			setEnv:   false,
		},
		{
			name:     "multiple shard placeholders",
			input:    []byte("job-$(SHARD)-replica-$(SHARD)"),
			envVar:   "3",
			expected: []byte("job-3-replica-3"),
			setEnv:   true,
		},
		{
			name:     "no shard placeholder",
			input:    []byte("static-config"),
			expected: []byte("static-config"),
			setEnv:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lookup func(string) (string, bool)
			if tt.setEnv {
				lookup = func(_ string) (string, bool) {
					return tt.envVar, true
				}
			} else {
				lookup = func(_ string) (string, bool) {
					return "", false
				}
			}

			result := instantiateShard(tt.input, lookup)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetScrapeConfigsResponse_InvalidURL(t *testing.T) {
	httpClient := &http.Client{}
	invalidURL := "://invalid-url"

	_, err := getScrapeConfigsResponse(httpClient, invalidURL)
	assert.Error(t, err)
}

func TestGetScrapeConfigsResponse_HTTPError(t *testing.T) {
	// Create a test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	httpClient := &http.Client{}

	config, err := getScrapeConfigsResponse(httpClient, server.URL)
	// This should succeed in reading the response but return empty config
	assert.NoError(t, err)
	assert.Empty(t, config)
}

func TestGetScrapeConfigsResponse_InvalidYAML(t *testing.T) {
	// Create a test server that returns invalid YAML
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid: yaml: content: [[["))
	}))
	defer server.Close()

	httpClient := &http.Client{}

	_, err := getScrapeConfigsResponse(httpClient, server.URL)
	assert.Error(t, err)
}

func writeCAFile(t *testing.T, path string, server *httptest.Server) {
	t.Helper()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, caPEM, 0o600))
}

func newTLSTestManager(t *testing.T, setup ...func(*Manager, *httptest.Server)) (*Manager, *observer.ObservedLogs, string) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/scrape_configs" {
			_, _ = w.Write([]byte("{}"))
		}
	}))
	t.Cleanup(server.Close)

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	writeCAFile(t, caFile, server)

	cfg := &Config{
		Interval:    time.Hour,
		CollectorID: "test-collector",
	}
	cfg.ClientConfig.Endpoint = server.URL
	cfg.ClientConfig.TLS.CAFile = caFile
	promCfg, err := promconfig.Load("", nil)
	require.NoError(t, err)

	core, logs := observer.New(zapcore.InfoLevel)
	settings := receivertest.NewNopSettings(metadata.Type)
	settings.Logger = zap.New(core)

	ctx := t.Context()
	promLogger := promslog.NewNopLogger()
	reg := prometheus.NewRegistry()
	sdMetrics, err := discovery.CreateAndRegisterSDMetrics(reg)
	require.NoError(t, err)
	discoveryManager := discovery.NewManager(ctx, promLogger, reg, sdMetrics)
	require.NotNil(t, discoveryManager)
	scrapeManager, err := scrape.NewManager(&scrape.Options{}, promLogger, nil, nil, teststorage.New(t), reg)
	require.NoError(t, err)
	t.Cleanup(scrapeManager.Stop)

	manager := NewManager(settings, cfg, sharedpromconfig.NewConfig(promCfg))
	manager.tlsReloadDelay = 10 * time.Millisecond
	for _, f := range setup {
		f(manager, server)
	}
	require.NoError(t, manager.Start(ctx, componenttest.NewNopHost(), scrapeManager, discoveryManager))
	t.Cleanup(manager.Shutdown)
	return manager, logs, manager.cfg.ClientConfig.TLS.CAFile
}

func TestSetHTTPClientFailsClosedOnError(t *testing.T) {
	manager, _, caFile := newTLSTestManager(t)
	validCA, err := os.ReadFile(caFile)
	require.NoError(t, err)
	require.NotNil(t, manager.httpClient.Load())

	require.NoError(t, manager.watcher.Remove(filepath.Dir(caFile)))
	require.NoError(t, os.WriteFile(caFile, []byte("not a certificate"), 0o600))

	require.Error(t, manager.setHTTPClient(t.Context()))
	assert.Nil(t, manager.httpClient.Load())
	_, err = manager.sync(0)
	require.ErrorIs(t, err, errNoHTTPClient)

	require.NoError(t, os.WriteFile(caFile, validCA, 0o600))
	require.NoError(t, manager.setHTTPClient(t.Context()))
	_, err = manager.sync(0)
	assert.NoError(t, err)
}

func TestSyncDuringTLSFileRotation(t *testing.T) {
	manager, _, caFile := newTLSTestManager(t)
	validCA, err := os.ReadFile(caFile)
	require.NoError(t, err)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			content := validCA
			if i%2 == 1 {
				content = []byte("not a certificate")
			}
			_ = os.WriteFile(caFile, content, 0o600)
			time.Sleep(time.Millisecond)
		}
	})
	for range 200 {
		if _, syncErr := manager.sync(0); syncErr != nil {
			assert.ErrorIs(t, syncErr, errNoHTTPClient)
		}
	}
	close(done)
	wg.Wait()

	require.NoError(t, os.WriteFile(caFile, []byte("not a certificate"), 0o600))
	require.Eventually(t, func() bool {
		_, syncErr := manager.sync(0)
		return errors.Is(syncErr, errNoHTTPClient)
	}, 5*time.Second, 10*time.Millisecond, "a failed rebuild must stop syncing")
	require.NoError(t, os.WriteFile(caFile, validCA, 0o600))
	require.Eventually(t, func() bool {
		_, syncErr := manager.sync(0)
		return syncErr == nil
	}, 5*time.Second, 10*time.Millisecond, "a good rebuild must resume syncing")
}

// writeSecretVolume updates dir the way the kubelet updates a mounted Secret:
// files go into a new timestamped directory, ..data is swapped to it with a
// rename, and the previous directory is removed.
func writeSecretVolume(t *testing.T, dir, generation string, files map[string][]byte) {
	t.Helper()
	tsDir := filepath.Join(dir, "..ts_"+generation)
	require.NoError(t, os.Mkdir(tsDir, 0o700))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(tsDir, name), content, 0o600))
	}
	previous, _ := os.Readlink(filepath.Join(dir, "..data"))
	require.NoError(t, os.Symlink(filepath.Base(tsDir), filepath.Join(dir, "..data_tmp")))
	require.NoError(t, os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")))
	for name := range files {
		link := filepath.Join(dir, name)
		if _, err := os.Lstat(link); os.IsNotExist(err) {
			require.NoError(t, os.Symlink(filepath.Join("..data", name), link))
		}
	}
	if previous != "" {
		require.NoError(t, os.RemoveAll(filepath.Join(dir, previous)))
	}
}

func TestTLSWatcherSeesRepeatedSecretRotations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs extra privileges on Windows")
	}
	var caPEM []byte
	dir := t.TempDir()
	manager, logs, _ := newTLSTestManager(t, func(m *Manager, server *httptest.Server) {
		caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		writeSecretVolume(t, dir, "1", map[string][]byte{"ca.crt": caPEM})
		m.cfg.ClientConfig.TLS.CAFile = filepath.Join(dir, "ca.crt")
	})
	const rebuilt = "TLS files changed; rebuilding target allocator client"

	for i, generation := range []string{"2", "3"} {
		previous := manager.httpClient.Load()
		writeSecretVolume(t, dir, generation, map[string][]byte{"ca.crt": caPEM})

		require.Eventually(t, func() bool {
			return manager.httpClient.Load() != previous
		}, 5*time.Second, 10*time.Millisecond, "rotation %s not picked up", generation)
		assert.Never(t, func() bool {
			return logs.FilterMessage(rebuilt).Len() > i+1
		}, 10*manager.tlsReloadDelay, 10*time.Millisecond, "rotation %s rebuilt the client more than once", generation)
		_, err := manager.sync(0)
		require.NoError(t, err)
	}
}

func TestTLSWatcherCoalescesBurstOfChanges(t *testing.T) {
	manager, logs, caFile := newTLSTestManager(t, func(m *Manager, _ *httptest.Server) {
		m.tlsReloadDelay = 200 * time.Millisecond
	})
	validCA, err := os.ReadFile(caFile)
	require.NoError(t, err)
	const rebuilt = "TLS files changed; rebuilding target allocator client"

	for range 5 {
		require.NoError(t, os.WriteFile(caFile, validCA, 0o600))
	}
	require.Eventually(t, func() bool {
		return logs.FilterMessage(rebuilt).Len() == 1
	}, 5*time.Second, 10*time.Millisecond)
	assert.Never(t, func() bool {
		return logs.FilterMessage(rebuilt).Len() > 1
	}, 3*manager.tlsReloadDelay, 10*time.Millisecond)
}

func TestStartReturnsWithinIntervalWhenTargetAllocatorFails(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("not: [valid"))
	}))
	t.Cleanup(server.Close)

	cfg := &Config{
		Interval:    300 * time.Millisecond,
		CollectorID: "test-collector",
	}
	cfg.ClientConfig.Endpoint = server.URL
	promCfg, err := promconfig.Load("", nil)
	require.NoError(t, err)

	ctx := t.Context()
	promLogger := promslog.NewNopLogger()
	reg := prometheus.NewRegistry()
	sdMetrics, err := discovery.CreateAndRegisterSDMetrics(reg)
	require.NoError(t, err)
	discoveryManager := discovery.NewManager(ctx, promLogger, reg, sdMetrics)
	scrapeManager, err := scrape.NewManager(&scrape.Options{}, promLogger, nil, nil, teststorage.New(t), reg)
	require.NoError(t, err)
	t.Cleanup(scrapeManager.Stop)

	manager := NewManager(receivertest.NewNopSettings(metadata.Type), cfg, sharedpromconfig.NewConfig(promCfg))
	manager.newBackOff = func() backoff.BackOff { return backoff.NewConstantBackOff(20 * time.Millisecond) }
	t.Cleanup(manager.Shutdown)

	started := make(chan error, 1)
	go func() { started <- manager.Start(ctx, componenttest.NewNopHost(), scrapeManager, discoveryManager) }()
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "Start did not return after the target allocator kept failing")
	}
	assert.Greater(t, requests.Load(), int64(1), "the initial sync should retry before giving up")
}
