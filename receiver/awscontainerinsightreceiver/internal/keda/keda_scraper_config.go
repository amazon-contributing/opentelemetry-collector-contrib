// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package keda // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/keda"

import (
	"os"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/prometheus/prometheus/model/relabel"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/prometheusscraper"
)

const (
	caFile                    = "/etc/amazon-cloudwatch-observability-agent-cert/tls-ca.crt"
	collectionInterval        = 60 * time.Second
	jobName                   = "containerInsightsKedaScraper"
	scraperMetricsPath        = "/metrics"
	scraperK8sServiceSelector = "app.kubernetes.io/name=keda-operator"
)

func GetKedaScrapeConfig(hostinfo prometheusscraper.HostInfoProvider) *config.ScrapeConfig {
	scrapeConfig := &config.ScrapeConfig{
		ScrapeProtocols:        config.DefaultScrapeProtocols,
		ScrapeFallbackProtocol: config.PrometheusText0_0_4,
		ScrapeInterval:         model.Duration(collectionInterval),
		ScrapeTimeout:          model.Duration(collectionInterval),
		JobName:                jobName,
		Scheme:                 "http",
		MetricsPath:            scraperMetricsPath,
		ServiceDiscoveryConfigs: discovery.Configs{
			&kubernetes.SDConfig{
				Role: kubernetes.RoleService,
				NamespaceDiscovery: kubernetes.NamespaceDiscovery{
					Names: []string{"keda"},
				},
				Selectors: []kubernetes.SelectorConfig{
					{
						Role:  kubernetes.RoleService,
						Label: scraperK8sServiceSelector,
					},
				},
			},
		},
		RelabelConfigs: []*relabel.Config{
			{
				SourceLabels: model.LabelNames{"__address__"},
				TargetLabel:  "__address__",
				Regex:        relabel.MustNewRegexp("([^:]+)(?:\\d+)?"),
				Replacement:  "${1}:8080",
				Action:       relabel.Replace,
			},
		},
		MetricRelabelConfigs: GetKedaMetricRelabelConfigs(hostinfo),
	}
	return scrapeConfig
}

func GetKedaMetricRelabelConfigs(hostinfo prometheusscraper.HostInfoProvider) []*relabel.Config {
	return []*relabel.Config{
		{
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("keda_scaler_.*|keda_scaled_object_.*|keda_internal_scale_loop_latency_seconds|keda_internal_metricsservice_grpc_server_handled_total"),
			Action:       relabel.Keep,
		},
		{
			SourceLabels: model.LabelNames{"scaledObject"},
			TargetLabel:  "ScaledObject",
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"scaler"},
			TargetLabel:  "Scaler",
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"metric"},
			TargetLabel:  "Metric",
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"namespace"},
			TargetLabel:  ci.K8sNamespace,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  ci.NodeNameKey,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  os.Getenv("HOST_NAME"),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  ci.ClusterNameKey,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  hostinfo.GetClusterName(),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  ci.InstanceID,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  hostinfo.GetInstanceID(),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  ci.InstanceType,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  hostinfo.GetInstanceType(),
			Action:       relabel.Replace,
		},
	}
}
