// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package karpenter

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
	jobName                   = "containerInsightsKarpenterScraper"
	scraperMetricsPath        = "/metrics"
	scraperK8sServiceSelector = "app.kubernetes.io/name=karpenter"
)

func GetKarpenterScrapeConfig(hostinfo prometheusscraper.HostInfoProvider) *config.ScrapeConfig {
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
					Names: []string{"karpenter"},
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
				Regex:        relabel.MustNewRegexp("([^:]+)(?::\\d+)?"),
				Replacement:  "${1}:8000",
				Action:       relabel.Replace,
			},
		},
		MetricRelabelConfigs: GetKarpenterMetricRelabelConfigs(hostinfo),
	}

	return scrapeConfig
}

func GetKarpenterMetricRelabelConfigs(hostinfo prometheusscraper.HostInfoProvider) []*relabel.Config {
	return []*relabel.Config{
		{
			// Drop Go runtime metrics that are often histograms
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("go_.*"),
			Action:       relabel.Drop,
		},
		{
			// Drop Prometheus client metrics that are often histograms
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("promhttp_.*"),
			Action:       relabel.Drop,
		},
		{
			// Drop controller runtime metrics that can be problematic
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("controller_runtime_.*_bucket$|workqueue_.*_bucket$"),
			Action:       relabel.Drop,
		},
		{
			// Drop problematic histogram bucket metrics but keep _total and summary metrics
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp(".*_bucket$|.*_seconds_bucket$|.*_duration_seconds_bucket$"),
			Action:       relabel.Drop,
		},
		{
			// Drop specific summary metrics that cause panics in the containerinsight utils
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("karpenter_nodes_termination_time_seconds$"),
			Action:       relabel.Drop,
		},
		{
			// Keep all Karpenter metrics AND controller_runtime metrics (after dropping problematic ones above)
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("karpenter_nodes_leases_deleted|karpenter_interruption_deleted_messages|karpenter_deprovisioning_replacement_machine_initialized_seconds_sum|karpenter_deprovisioning_replacement_machine_initialized_seconds_count|karpenter_disruption_replacement_nodeclaim_initialized_seconds_sum|karpenter_disruption_replacement_nodeclaim_initialized_seconds_count|karpenter_interruption_message_latency_time_seconds_sum|karpenter_interruption_message_latency_time_seconds_count|karpenter_pods_startup_time_seconds_sum|karpenter_pods_startup_time_seconds_count|controller_runtime_reconcile_total|controller_runtime_active_workers|controller_runtime_reconcile_errors_total"),
			Action:       relabel.Keep,
		},
		{
			SourceLabels: model.LabelNames{"node"},
			TargetLabel:  "Node",
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"nodepool"},
			TargetLabel:  "NodePool",
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"provisioner"},
			TargetLabel:  "Provisioner",
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
