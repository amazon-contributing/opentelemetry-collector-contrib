// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package gpu

import (
	"time"

	configutil "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/prometheus/prometheus/model/relabel"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
)

const (
	caFile                    = "/etc/amazon-cloudwatch-observability-agent-cert/tls-ca.crt"
	collectionInterval        = 60 * time.Second
	jobName                   = "containerInsightsDCGMExporterScraper"
	scraperMetricsPath        = "/metrics"
	scraperK8sServiceSelector = "k8s-app=dcgm-exporter-service"
)

type hostInfoProvider interface {
	GetClusterName() string
	GetInstanceID() string
}

func GetScraperConfig(hostInfoProvider hostInfoProvider) *config.ScrapeConfig {
	return &config.ScrapeConfig{
		HTTPClientConfig: configutil.HTTPClientConfig{
			TLSConfig: configutil.TLSConfig{
				CAFile:             caFile,
				InsecureSkipVerify: false,
			},
		},
		ScrapeInterval: model.Duration(collectionInterval),
		ScrapeTimeout:  model.Duration(collectionInterval),
		JobName:        jobName,
		Scheme:         "https",
		MetricsPath:    scraperMetricsPath,
		ServiceDiscoveryConfigs: discovery.Configs{
			&kubernetes.SDConfig{
				Role: kubernetes.RoleService,
				NamespaceDiscovery: kubernetes.NamespaceDiscovery{
					IncludeOwnNamespace: true,
				},
				Selectors: []kubernetes.SelectorConfig{
					{
						Role:  kubernetes.RoleService,
						Label: scraperK8sServiceSelector,
					},
				},
			},
		},
		MetricRelabelConfigs: getMetricRelabelConfig(hostInfoProvider),
	}
}

func getMetricRelabelConfig(hostInfoProvider hostInfoProvider) []*relabel.Config {
	return []*relabel.Config{
		{
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("DCGM_.*"),
			Action:       relabel.Keep,
		},
		{
			SourceLabels: model.LabelNames{"Hostname"},
			TargetLabel:  ci.NodeNameKey,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"namespace"},
			TargetLabel:  ci.AttributeK8sNamespace,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		// hacky way to inject static values (clusterName & instanceId) to label set without additional processor
		// relabel looks up an existing label then creates another label with given key (TargetLabel) and value (static)
		{
			SourceLabels: model.LabelNames{"namespace"},
			TargetLabel:  ci.ClusterNameKey,
			Regex:        relabel.MustNewRegexp(".*"),
			Replacement:  hostInfoProvider.GetClusterName(),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"namespace"},
			TargetLabel:  ci.InstanceID,
			Regex:        relabel.MustNewRegexp(".*"),
			Replacement:  hostInfoProvider.GetInstanceID(),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"pod"},
			TargetLabel:  ci.AttributeFullPodName,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		// additional k8s podname for service name and k8s blob decoration
		{
			SourceLabels: model.LabelNames{"pod"},
			TargetLabel:  ci.AttributeK8sPodName,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"container"},
			TargetLabel:  ci.AttributeContainerName,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"device"},
			TargetLabel:  ci.AttributeGpuDevice,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
	}
}
