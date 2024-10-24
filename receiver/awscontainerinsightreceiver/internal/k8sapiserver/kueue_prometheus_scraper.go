// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sapiserver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/kueuepromscraper"

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	configutil "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/prometheus/prometheus/model/relabel"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"
)

const (
	kmCollectionInterval = 60 * time.Second
	// kmJobName needs to be "containerInsightsKueueMetricsScraper" so metric translator tags the source as the container insights receiver
	kmJobName                   = "containerInsightsKueueMetricsScraper"
	kueueNamespace              = "kueue-system"
	kueueNameLabelSelector      = "app.kubernetes.io/name=kueue"
	kueueComponentLabelSelector = "app.kubernetes.io/component=controller"
	kueueServiceFieldSelector   = "metadata.name=kueue-controller-manager-metrics-service"
)

var ( // list of regular expressions for the kueue metrics this scraper is intended to capture
	kueueMetricAllowList = []string{
		"^kueue_pending_workloads$",
		"^kueue_evicted_workloads_total$",
		"^kueue_admitted_active_workloads$",
		"^kueue_cluster_queue_resource_usage$",
		"^kueue_cluster_queue_nominal_quota$",
		"^kueue_cluster_queue_borrowing_limit$",
	}
	kueueMetricsAllowRegex = strings.Join(kueueMetricAllowList, "|")
)

type KueuePrometheusScraper struct {
	ctx                 context.Context
	settings            component.TelemetrySettings
	host                component.Host
	clusterNameProvider clusterNameProvider
	prometheusReceiver  receiver.Metrics
	running             bool
	leaderElection      *LeaderElection
}

type KueuePrometheusScraperOpts struct {
	Ctx                 context.Context
	TelemetrySettings   component.TelemetrySettings
	Consumer            consumer.Metrics
	Host                component.Host
	ClusterNameProvider clusterNameProvider
	BearerToken         string
	LeaderElection      *LeaderElection
}

func NewKueuePrometheusScraper(opts KueuePrometheusScraperOpts) (*KueuePrometheusScraper, error) {
	if opts.Consumer == nil {
		return nil, errors.New("consumer cannot be nil")
	}
	if opts.Host == nil {
		return nil, errors.New("host cannot be nil")
	}
	if opts.LeaderElection == nil {
		return nil, errors.New("leader election cannot be nil")
	}
	if opts.ClusterNameProvider == nil {
		return nil, errors.New("cluster name provider cannot be nil")
	}

	scrapeConfig := &config.ScrapeConfig{
		HTTPClientConfig: configutil.HTTPClientConfig{
			TLSConfig: configutil.TLSConfig{
				InsecureSkipVerify: true,
			},
		},
		ScrapeInterval:  model.Duration(kmCollectionInterval),
		ScrapeTimeout:   model.Duration(kmCollectionInterval),
		ScrapeProtocols: config.DefaultScrapeProtocols,
		JobName:         kmJobName,
		HonorTimestamps: true,
		Scheme:          "https",
		MetricsPath:     "/metrics",
		ServiceDiscoveryConfigs: discovery.Configs{
			&kubernetes.SDConfig{
				Role: kubernetes.RoleService,
				NamespaceDiscovery: kubernetes.NamespaceDiscovery{
					Names: []string{kueueNamespace},
				},
				Selectors: []kubernetes.SelectorConfig{
					{
						Role:  kubernetes.RoleService,
						Label: fmt.Sprintf("%s,%s", kueueNameLabelSelector, kueueComponentLabelSelector),
						Field: kueueServiceFieldSelector,
					},
				},
			},
		},
		MetricRelabelConfigs: []*relabel.Config{
			{ // filter by metric name: keep only the Kueue metrics specified via regex in `kueueMetricAllowList`
				Action:       relabel.Keep,
				Regex:        relabel.MustNewRegexp(kueueMetricsAllowRegex),
				SourceLabels: model.LabelNames{"__name__"},
			},
			// type conflicts with the log Type in the container insights output format.
			{ // add "kubernetes_type" to serve as non-conflicting name.
				Action:      relabel.LabelMap,
				Regex:       relabel.MustNewRegexp("^type$"),
				Replacement: "kubernetes_type",
			},
			{ // drop conflicting name "type"
				Action: relabel.LabelDrop,
				Regex:  relabel.MustNewRegexp("^type$"),
			},
			{ // add port to value of label "__address__" if it isn't already included.
				Action:       relabel.Replace,
				Regex:        relabel.MustNewRegexp("([^:]+)(?::\\d+)?;(\\d+)"),
				SourceLabels: model.LabelNames{"__address__", "__meta_kubernetes_service_annotation_prometheus_io_port"},
				Replacement:  "$1:$2",
				TargetLabel:  "__address__",
			},
			{ // add cluster name as a label
				Action:      relabel.Replace,
				Regex:       relabel.MustNewRegexp(".*"),
				TargetLabel: "ClusterName",
				Replacement: opts.ClusterNameProvider.GetClusterName(),
			},
		},
	}

	if opts.BearerToken != "" {
		scrapeConfig.HTTPClientConfig.BearerToken = configutil.Secret(opts.BearerToken)
	} else {
		opts.TelemetrySettings.Logger.Warn("bearer token is not set, kueue metrics will not be published")
	}

	promConfig := prometheusreceiver.Config{
		PrometheusConfig: &prometheusreceiver.PromConfig{
			ScrapeConfigs: []*config.ScrapeConfig{scrapeConfig},
		},
	}

	params := receiver.Settings{
		ID:                component.MustNewID(kmJobName),
		TelemetrySettings: opts.TelemetrySettings,
	}

	promFactory := prometheusreceiver.NewFactory()
	promReceiver, err := promFactory.CreateMetricsReceiver(opts.Ctx, params, &promConfig, opts.Consumer)
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus receiver for kueue metrics: %w", err)
	}

	return &KueuePrometheusScraper{
		ctx:                 opts.Ctx,
		settings:            opts.TelemetrySettings,
		host:                opts.Host,
		clusterNameProvider: opts.ClusterNameProvider,
		prometheusReceiver:  promReceiver,
		leaderElection:      opts.LeaderElection,
	}, nil
}

func (ps *KueuePrometheusScraper) GetMetrics() []pmetric.Metrics {
	// This method will never return metrics because the metrics are collected by the scraper.
	// This method will ensure the scraper is running
	if !ps.leaderElection.leading {
		return nil
	}

	// if we are leading, ensure we are running
	if !ps.running {
		ps.settings.Logger.Info("The Kueue metrics scraper is not running, starting up the scraper")
		err := ps.prometheusReceiver.Start(ps.ctx, ps.host)
		if err != nil {
			ps.settings.Logger.Error("Unable to start Kueue PrometheusReceiver", zap.Error(err))
		}
		ps.running = err == nil
	}
	return nil
}
func (ps *KueuePrometheusScraper) Shutdown() {
	if ps.running {
		err := ps.prometheusReceiver.Shutdown(ps.ctx)
		if err != nil {
			ps.settings.Logger.Error("Unable to shutdown Kueue PrometheusReceiver", zap.Error(err))
		}
		ps.running = false
	}
}
