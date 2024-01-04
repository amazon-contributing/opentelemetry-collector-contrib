// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package k8swindows // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows"

import (
	"fmt"
	"os"
	"strconv"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	cExtractor "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/host"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/extractors"
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

func (k *kubeletSummaryProvider) getMetrics() ([]*cExtractor.CAdvisorMetric, error) {
	summary, err := k.client.Summary(k.logger)
	if err != nil {
		k.logger.Error("kubelet summary API failed, ", zap.Error(err))
		return nil, err
	}

	return k.getPodMetrics(summary)
}

func (k *kubeletSummaryProvider) getContainerMetrics(summary *stats.Summary) ([]*cExtractor.CAdvisorMetric, error) {
	var metrics []*cExtractor.CAdvisorMetric
	// todo: implement CPU, memory metrics from containers
	return metrics, nil
}

func (k *kubeletSummaryProvider) getPodMetrics(summary *stats.Summary) ([]*cExtractor.CAdvisorMetric, error) {
	// todo: This is not complete implementation of pod level metric collection since network level metrics are pending
	// May need to add some more pod level labels for store decorators to work properly

	var metrics []*cExtractor.CAdvisorMetric

	for _, pod := range summary.Pods {
		k.logger.Info(fmt.Sprintf("pod summary %v", pod.PodRef.Name))

		tags := map[string]string{}

		tags[ci.PodIDKey] = pod.PodRef.UID
		tags[ci.K8sPodNameKey] = pod.PodRef.Name
		tags[ci.K8sNamespace] = pod.PodRef.Namespace
		tags[ci.Timestamp] = strconv.FormatInt(pod.CPU.Time.UnixNano(), 10)

		rawMetric := extractors.ConvertPodToRaw(&pod)
		for _, extractor := range GetMetricsExtractors() {
			if extractor.HasValue(rawMetric) {
				metrics = append(metrics, extractor.GetValue(rawMetric, &k.hostInfo, ci.TypePod)...)
			}
		}
		for _, metric := range metrics {
			metric.AddTags(tags)
		}
	}
	return metrics, nil
}

func (k *kubeletSummaryProvider) getNodeMetrics() ([]*cExtractor.CAdvisorMetric, error) {
	var metrics []*cExtractor.CAdvisorMetric
	//todo: Implement CPU, memory and network metrics at node
	return metrics, nil
}
