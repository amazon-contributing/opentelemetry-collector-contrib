// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"
	"unsafe"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	commonconfig "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	promconfig "github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/scrape"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/apiserver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/sharedpromconfig"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver/internal/targetallocator"
)

const (
	defaultGCInterval = 2 * time.Minute
	gcIntervalDelta   = 1 * time.Minute
)

// pReceiver is the type that provides Prometheus scraper/receiver functionality.
type pReceiver struct {
	cfg            *Config
	consumer       consumer.Metrics
	cancelFunc     context.CancelFunc
	configLoaded   chan struct{}
	loadConfigOnce sync.Once

	settings               receiver.Settings
	scrapeManager          *scrape.Manager
	discoveryManager       *discovery.Manager
	targetAllocatorManager *targetallocator.Manager
	apiServerManager       *apiserver.Manager
	registry               *prometheus.Registry
	registerer             prometheus.Registerer
	unregisterMetrics      func()
}

// sharedRegistries tracks every live receiver's registry so one caller can report all of them.
// Registration is per instance and keyed by receiver ID, but a single ID can have several live
// instances at once (some callers keep instances alive under one ID and retry Start on a timer),
// so each ID maps to an ordered list of its live registries. The first still-live instance is the
// one published, which keeps the receiver label unambiguous; when it shuts down the next live
// instance is published instead of the ID going dark, and re-creating a receiver never collides.
var (
	sharedMu         sync.Mutex
	sharedRegistries = map[string][]*prometheus.Registry{}
)

func joinShared(id string, reg *prometheus.Registry) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if slices.Contains(sharedRegistries[id], reg) {
		return
	}
	sharedRegistries[id] = append(sharedRegistries[id], reg)
}

func leaveShared(id string, reg *prometheus.Registry) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	regs := sharedRegistries[id]
	for i, existing := range regs {
		if existing == reg {
			regs = append(regs[:i], regs[i+1:]...)
			break
		}
	}
	if len(regs) == 0 {
		delete(sharedRegistries, id)
	} else {
		sharedRegistries[id] = regs
	}
}

// SharedGatherer reports the discovery and scrape registries of every running receiver in this
// process. The set is resolved at gather time, so receivers starting or stopping later are picked
// up without re-wiring, and each series keeps the receiver label identifying its origin. When more
// than one live instance shares a receiver ID, the first still-live instance is published to keep
// the receiver label unambiguous.
func SharedGatherer() prometheus.Gatherer {
	return prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		sharedMu.Lock()
		all := make(prometheus.Gatherers, 0, len(sharedRegistries))
		for _, regs := range sharedRegistries {
			if len(regs) > 0 {
				all = append(all, regs[0])
			}
		}
		sharedMu.Unlock()
		return all.Gather()
	})
}

// New creates a new prometheus.Receiver reference.
func newPrometheusReceiver(set receiver.Settings, cfg *Config, next consumer.Metrics) (*pReceiver, error) {
	if err := cfg.PrometheusConfig.Reload(); err != nil {
		return nil, fmt.Errorf("failed to reload Prometheus config: %w", err)
	}

	baseCfg := promconfig.Config(*cfg.PrometheusConfig)
	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWith(
		prometheus.Labels{"receiver": set.ID.String()},
		registry,
	)
	sharedCfg := sharedpromconfig.NewConfig(&baseCfg)
	var apiServerManager *apiserver.Manager
	if cfg.APIServer.Enabled {
		apiServerManager = apiserver.NewManager(
			set,
			&cfg.APIServer,
			sharedCfg,
			registry,
			registerer,
		)
	}

	pr := &pReceiver{
		cfg:          cfg,
		consumer:     next,
		settings:     set,
		configLoaded: make(chan struct{}),
		registerer:   registerer,
		registry:     registry,
		targetAllocatorManager: targetallocator.NewManager(
			set,
			cfg.TargetAllocator.Get(),
			sharedCfg,
		),
		apiServerManager: apiServerManager,
	}
	return pr, nil
}

// Start is the method that starts Prometheus scraping. It
// is controlled by having previously defined a Configuration using perhaps New.
func (r *pReceiver) Start(ctx context.Context, host component.Host) error {
	return r.start(ctx, host, prometheusComponentTestOptions{})
}

func (r *pReceiver) start(ctx context.Context, host component.Host, opts prometheusComponentTestOptions) error {
	discoveryCtx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel

	logger := slog.New(zapslog.NewHandler(r.settings.Logger.Core()))

	err := r.initPrometheusComponents(discoveryCtx, logger, host, opts)
	if err != nil {
		r.settings.Logger.Error("Failed to initPrometheusComponents Prometheus components", zap.Error(err))
		return err
	}

	err = r.targetAllocatorManager.Start(ctx, host, r.scrapeManager, r.discoveryManager)
	if err != nil {
		return err
	}

	r.loadConfigOnce.Do(func() {
		close(r.configLoaded)
	})

	if r.apiServerManager != nil {
		err := r.apiServerManager.Start(ctx, host, r.scrapeManager)
		if err != nil {
			r.settings.Logger.Error("Failed to start APIServer", zap.Error(err))
		}
	}

	// Register only after the fallible start steps above have run, so a failed Start (which the
	// collector may not pair with a Shutdown call) never leaks this registry into the shared set.
	joinShared(r.settings.ID.String(), r.registry)
	return nil
}

func (r *pReceiver) initPrometheusComponents(
	ctx context.Context, logger *slog.Logger, host component.Host,
	opts prometheusComponentTestOptions,
) error {
	// Register the metrics needed by service discovery mechanisms.
	sdMetrics, err := discovery.CreateAndRegisterSDMetrics(r.registerer)
	if err != nil {
		return fmt.Errorf("failed to register service discovery metrics: %w", err)
	}

	var discoveryManagerTestOptions []func(*discovery.Manager)
	if opts.discovery.updatert > 0 {
		discoveryManagerTestOptions = append(discoveryManagerTestOptions, discovery.Updatert(opts.discovery.updatert))
	}
	r.discoveryManager = discovery.NewManager(ctx, logger, r.registerer, sdMetrics, discoveryManagerTestOptions...)
	if r.discoveryManager == nil {
		// NewManager can sometimes return nil if it encountered an error, but
		// the error message is logged separately.
		return errors.New("failed to create discovery manager")
	}

	go func() {
		r.settings.Logger.Info("Starting discovery manager")
		if err = r.discoveryManager.Run(); err != nil && !errors.Is(err, context.Canceled) {
			r.settings.Logger.Error("Discovery manager failed", zap.Error(err))
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(err))
		}
	}()

	store, err := internal.NewAppendable(
		r.consumer,
		r.settings,
		!r.cfg.ignoreMetadata,
		r.cfg.PrometheusConfig.GlobalConfig.ExternalLabels,
		r.cfg.TrimMetricSuffixes,
	)
	if err != nil {
		return err
	}

	scrapeOpts := r.initScrapeOptions(opts.scrape)

	// for testing only
	if r.cfg.skipOffsetting {
		optsValue := reflect.ValueOf(scrapeOpts).Elem()
		field := optsValue.FieldByName("skipJitterOffsetting")
		reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).
			Elem().
			Set(reflect.ValueOf(true))
	}

	scrapeManager, err := scrape.NewManager(scrapeOpts, logger, nil, nil, store, r.registerer)
	if err != nil {
		return err
	}
	r.scrapeManager = scrapeManager

	r.unregisterMetrics = func() {
		sdMetrics.RefreshManager.Unregister()
		for _, sdMetric := range sdMetrics.MechanismMetrics {
			sdMetric.Unregister()
		}
		r.discoveryManager.UnregisterMetrics()
		r.scrapeManager.UnregisterMetrics()
	}

	go func() {
		// The scrape manager needs to wait for the configuration to be loaded before beginning
		<-r.configLoaded
		r.settings.Logger.Info("Starting scrape manager")
		if err := r.scrapeManager.Run(r.discoveryManager.SyncCh()); err != nil {
			r.settings.Logger.Error("Scrape manager failed", zap.Error(err))
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(err))
		}
	}()

	return nil
}

func (r *pReceiver) initScrapeOptions(o prometheusScrapeTestOptions) *scrape.Options {
	opts := &scrape.Options{
		DiscoveryReloadInterval: model.Duration(o.discoveryReloadInterval),
		PassMetadataInContext:   true,
		HTTPClientOptions: []commonconfig.HTTPClientOption{
			commonconfig.WithUserAgent(r.settings.BuildInfo.Command + "/" + r.settings.BuildInfo.Version),
		},
		EnableStartTimestampZeroIngestion: metadata.ReceiverPrometheusreceiverEnableCreatedTimestampZeroIngestionFeatureGate.IsEnabled(),
		ScrapeOnShutdown:                  r.cfg.ScrapeOnShutdown,
		DiscoveryReloadOnStartup:          r.cfg.DiscoveryReloadOnStartup,
		InitialScrapeOffset:               r.cfg.InitialScrapeOffset,
	}

	return opts
}

// gcInterval returns the longest scrape interval used by a scrape config,
// plus a delta to prevent race conditions.
// This ensures jobs are not garbage collected between scrapes.
func gcInterval(cfg *PromConfig) time.Duration {
	gcInterval := max(time.Duration(cfg.GlobalConfig.ScrapeInterval)+gcIntervalDelta, defaultGCInterval)
	for _, scrapeConfig := range cfg.ScrapeConfigs {
		if time.Duration(scrapeConfig.ScrapeInterval)+gcIntervalDelta > gcInterval {
			gcInterval = time.Duration(scrapeConfig.ScrapeInterval) + gcIntervalDelta
		}
	}
	return gcInterval
}

// Shutdown stops and cancels the underlying Prometheus scrapers.
func (r *pReceiver) Shutdown(ctx context.Context) error {
	leaveShared(r.settings.ID.String(), r.registry)
	if r.cancelFunc != nil {
		r.cancelFunc()
	}
	if r.scrapeManager != nil {
		r.scrapeManager.Stop()
	}
	if r.targetAllocatorManager != nil {
		r.targetAllocatorManager.Shutdown()
	}
	if r.unregisterMetrics != nil {
		r.unregisterMetrics()
	}
	if r.apiServerManager != nil {
		if err := r.apiServerManager.Shutdown(ctx); err != nil {
			return err
		}
	}
	return nil
}

type prometheusComponentTestOptions struct {
	discovery prometheusDiscoveryTestOptions
	scrape    prometheusScrapeTestOptions
}

type prometheusDiscoveryTestOptions struct {
	// updatert is the interval for updating targets.
	//
	// If zero, the default (5s) from Prometheus is used.
	// This option is primarily for testing.
	updatert time.Duration
}

type prometheusScrapeTestOptions struct {
	// discoveryReloadInterval is the interval for reloading
	// scrape configurations.
	//
	// If zero, the default (5s) from Prometheus is used.
	// This option is primarily for testing.
	discoveryReloadInterval time.Duration
}
