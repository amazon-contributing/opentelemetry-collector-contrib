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
	return &config.ScrapeConfig{
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
}

func GetKarpenterMetricRelabelConfigs(hostinfo prometheusscraper.HostInfoProvider) []*relabel.Config {
	return []*relabel.Config{
		{
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("karpenter_.*"),
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