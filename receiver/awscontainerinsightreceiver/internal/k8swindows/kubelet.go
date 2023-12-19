// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package k8swindows // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows"

import (
	"fmt"
	"os"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/host"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"

	"go.uber.org/zap"
	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

type kubeletSummaryProvider struct {
	logger   *zap.Logger
	hostIP   string
	hostPort string
	client   *kubeletutil.KubeletClient
	hostInfo host.Info
}

func new(logger *zap.Logger, info host.Info) (*kubeletSummaryProvider, error) {
	hostIP := os.Getenv("HOST_IP")
	kclient, err := kubeletutil.NewKubeletClient(hostIP, ci.KubeSecurePort, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize kubelet client: %w", err)
	}
	return &kubeletSummaryProvider{
		logger:   logger,
		client:   kclient,
		hostInfo: info,
	}, nil
}

func (k *kubeletSummaryProvider) getMetrics() ([]*extractors.CAdvisorMetric, error) {
	summary, err := k.client.Summary(k.logger)
	if err != nil {
		k.logger.Error("kubelet summary API failed, ", zap.Error(err))
		return nil, err
	}

	return k.getPodMetrics(summary)
}

func (k *kubeletSummaryProvider) getContainerMetrics(summary *stats.Summary) ([]*extractors.CAdvisorMetric, error) {
	var metrics []*extractors.CAdvisorMetric
	// todo: implement CPU, memory metrics from containers
	return metrics, nil
}

func (k *kubeletSummaryProvider) getPodMetrics(summary *stats.Summary) ([]*extractors.CAdvisorMetric, error) {
	// todo: This is not complete implementation of pod level metric collection since network level metrics are pending
	// May need to add some more pod level labels for store decorators to work properly

	var metrics []*extractors.CAdvisorMetric

	nodeCPUCores := k.hostInfo.GetNumCores()
	for _, pod := range summary.Pods {
		k.logger.Info(fmt.Sprintf("pod summary %v", pod.PodRef.Name))
		metric := extractors.NewCadvisorMetric(ci.TypePod, k.logger)

		metric.AddField(ci.PodIDKey, pod.PodRef.UID)
		metric.AddField(ci.K8sPodNameKey, pod.PodRef.Name)
		metric.AddField(ci.K8sNamespace, pod.PodRef.Namespace)

		// CPU metric
		metric.AddField(ci.MetricName(ci.TypePod, ci.CPUTotal), *pod.CPU.UsageCoreNanoSeconds)
		metric.AddField(ci.MetricName(ci.TypePod, ci.CPUUtilization), float64(*pod.CPU.UsageCoreNanoSeconds)/float64(nodeCPUCores))

		// Memory metrics
		metric.AddField(ci.MetricName(ci.TypePod, ci.MemUsage), *pod.Memory.UsageBytes)
		metric.AddField(ci.MetricName(ci.TypePod, ci.MemRss), *pod.Memory.RSSBytes)
		metric.AddField(ci.MetricName(ci.TypePod, ci.MemWorkingset), *pod.Memory.WorkingSetBytes)
		metric.AddField(ci.MetricName(ci.TypePod, ci.MemReservedCapacity), k.hostInfo.GetMemoryCapacity())
		metric.AddField(ci.MetricName(ci.TypePod, ci.MemUtilization), float64(*pod.Memory.WorkingSetBytes)/float64(k.hostInfo.GetMemoryCapacity())*100)
		metrics = append(metrics, metric)
	}
	return metrics, nil
}

func (k *kubeletSummaryProvider) getNodeMetrics() ([]*extractors.CAdvisorMetric, error) {
	var metrics []*extractors.CAdvisorMetric
	//todo: Implement CPU, memory and network metrics at node
	return metrics, nil
}
