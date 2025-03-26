// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package nvme // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/gpu"

import (
	"os"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/kubernetes"
	"github.com/prometheus/prometheus/model/relabel"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
)

const (
	collectionInterval        = 60 * time.Second
	jobName                   = "containerInsightsNVMeExporterScraper"
	scraperMetricsPath        = "/metrics"
	scraperK8sServiceSelector = "app=ebs-csi-node"
)

type hostInfoProvider interface {
	GetClusterName() string
	GetInstanceID() string
	GetInstanceType() string
}

func GetScraperConfig(hostInfoProvider hostInfoProvider) *config.ScrapeConfig {
	return &config.ScrapeConfig{
		ScrapeInterval:  model.Duration(collectionInterval),
		ScrapeTimeout:   model.Duration(collectionInterval),
		ScrapeProtocols: config.DefaultScrapeProtocols,
		JobName:         jobName,
		Scheme:          "http",
		MetricsPath:     scraperMetricsPath,
		ServiceDiscoveryConfigs: discovery.Configs{
			&kubernetes.SDConfig{
				Role: kubernetes.RoleService,
				NamespaceDiscovery: kubernetes.NamespaceDiscovery{
					Names: []string{"kube-system"},
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
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsReadOpsTotal),
			Replacement:  nodeReadOpsTotal,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsWriteOpsTotal),
			Replacement:  nodeWriteOpsTotal,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsReadBytesTotal),
			Replacement:  nodeReadBytesTotal,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsWriteBytesTotal),
			Replacement:  nodeWriteBytesTotal,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsReadTime),
			Replacement:  nodeReadTime,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsWriteTime),
			Replacement:  nodeWriteTime,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsExceededIOPSTime),
			Replacement:  nodeExceededIOPSTime,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsExceededTPTime),
			Replacement:  nodeExceededTPTime,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsExceededEC2IOPSTime),
			Replacement:  nodeExceededEC2IOPSTime,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsExceededEC2TPTime),
			Replacement:  nodeExceededEC2TPTime,
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			TargetLabel:  "__name__",
			Regex:        relabel.MustNewRegexp(ebsVolumeQueueLength),
			Replacement:  nodeVolumeQueueLength,
			Action:       relabel.Replace,
		},

		// Below metrics are historgram which are not supported for container insights yet
		{
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("aws_ebs_csi_write_io_latency_seconds_.*"),
			Action:       relabel.Drop,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("aws_ebs_csi_read_io_latency_seconds_.*"),
			Action:       relabel.Drop,
		},
		{
			SourceLabels: model.LabelNames{"__name__"},
			Regex:        relabel.MustNewRegexp("aws_ebs_csi_nvme_collector_duration_seconds.*"),
			Action:       relabel.Drop,
		},

		// Hacky way to inject static values (clusterName/instanceId/nodeName/volumeID)
		{
			SourceLabels: model.LabelNames{"instance_id"},
			TargetLabel:  ci.NodeNameKey,
			Regex:        relabel.MustNewRegexp(".*"),
			Replacement:  os.Getenv("HOST_NAME"),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"instance_id"},
			TargetLabel:  ci.ClusterNameKey,
			Regex:        relabel.MustNewRegexp(".*"),
			Replacement:  hostInfoProvider.GetClusterName(),
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"instance_id"},
			TargetLabel:  ci.InstanceID,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
		{
			SourceLabels: model.LabelNames{"volume_id"},
			TargetLabel:  ci.VolumeID,
			Regex:        relabel.MustNewRegexp("(.*)"),
			Replacement:  "${1}",
			Action:       relabel.Replace,
		},
	}
}
