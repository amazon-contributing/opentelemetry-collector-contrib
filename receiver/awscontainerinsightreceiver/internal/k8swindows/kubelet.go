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
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"

	stats "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

// KubeletProvider Represents interface to kubelet.
type KubeletProvider interface {
	GetClient() (*kubeletutil.KubeletClient, error)
	GetSummary() (*stats.Summary, error)
}

type kubeletProvider struct {
	logger *zap.Logger
	hostIP string
	port   string
	client *kubeletutil.KubeletClient
}

// GetClient Returns singleton kubelet client.
func (k *kubeletProvider) GetClient() (*kubeletutil.KubeletClient, error) {
	if k.client != nil {
		return k.client, nil
	}
	kclient, err := kubeletutil.NewKubeletClient(k.hostIP, k.port, k.logger)
	if err != nil {
		k.logger.Error("failed to initialize kubeletProvider client, ", zap.Error(err))
		return nil, err
	}
	k.client = kclient
	return kclient, nil
}

// GetSummary Get Summary from kubelet API.
func (k *kubeletProvider) GetSummary() (*stats.Summary, error) {
	kclient, err := k.GetClient()
	if err != nil {
		k.logger.Error("failed to get kubeletProvider client, ", zap.Error(err))
		return nil, err
	}

	summary, err := kclient.Summary(k.logger)
	if err != nil {
		k.logger.Error("kubeletProvider summary API failed, ", zap.Error(err))
		return nil, err
	}
	return summary, nil
}

func createDefaultKubeletProvider(logger *zap.Logger) KubeletProvider {
	kSP := &kubeletProvider{logger: logger, hostIP: os.Getenv("HOST_IP"), port: ci.KubeSecurePort}
	return kSP
}

// kubelet represents receiver to get metrics from kubelet.
type kubelet struct {
	logger          *zap.Logger
	kubeletProvider KubeletProvider
	hostInfo        cExtractor.CPUMemInfoProvider
}

// options decorates kubelet struct.
type options func(provider *kubelet)

func new(logger *zap.Logger, info cExtractor.CPUMemInfoProvider, opts ...options) (*kubelet, error) {
	kSP := &kubelet{
		logger:          logger,
		hostInfo:        info,
		kubeletProvider: createDefaultKubeletProvider(logger),
	}

	for _, opt := range opts {
		opt(kSP)
	}
	return kSP, nil
}

func (k *kubelet) getMetrics() ([]*cExtractor.CAdvisorMetric, error) {
	var metrics []*cExtractor.CAdvisorMetric

	summary, err := k.kubeletProvider.GetSummary()
	if err != nil {
		k.logger.Error("kubeletProvider summary API failed, ", zap.Error(err))
		return nil, err
	}
	outMetrics, err := k.getPodMetrics(summary)
	if err != nil {
		k.logger.Error("kubelet pod summary metrics failed, ", zap.Error(err))
		return metrics, err
	}
	metrics = append(metrics, outMetrics...)

	nodeMetics, err := k.getNodeMetrics(summary)
	if err != nil {
		k.logger.Error("kubelet node summary metrics failed, ", zap.Error(err))
		return nodeMetics, err
	}
	metrics = append(metrics, nodeMetics...)

	return metrics, nil
}

// getContainerMetrics returns container level metrics from kubelet.
func (k *kubelet) getContainerMetrics(pod *stats.PodStats) ([]*cExtractor.CAdvisorMetric, error) {
	var metrics []*cExtractor.CAdvisorMetric

	for _, container := range pod.Containers {
		tags := map[string]string{}

		tags[ci.PodIDKey] = pod.PodRef.UID
		tags[ci.K8sPodNameKey] = pod.PodRef.Name
		tags[ci.K8sNamespace] = pod.PodRef.Namespace
		tags[ci.ContainerNamekey] = container.Name
		containerID := fmt.Sprintf("%s-%s", pod.PodRef.UID, container.Name)
		tags[ci.ContainerIDkey] = containerID

		rawMetric := extractors.ConvertContainerToRaw(container, pod)
		tags[ci.Timestamp] = strconv.FormatInt(rawMetric.Time.UnixNano(), 10)
		for _, extractor := range GetMetricsExtractors() {
			if extractor.HasValue(rawMetric) {
				metrics = append(metrics, extractor.GetValue(rawMetric, k.hostInfo, ci.TypeContainer)...)
			}
		}
		for _, metric := range metrics {
			metric.AddTags(tags)
		}
	}
	return metrics, nil
}

// getPodMetrics returns pod and container level metrics from kubelet.
func (k *kubelet) getPodMetrics(summary *stats.Summary) ([]*cExtractor.CAdvisorMetric, error) {
	// todo: This is not complete implementation of pod level metric collection since network level metrics are pending
	// May need to add some more pod level labels for store decorators to work properly
	var metrics []*cExtractor.CAdvisorMetric

	for _, pod := range summary.Pods {
		var metricsPerPod []*cExtractor.CAdvisorMetric

		tags := map[string]string{}

		tags[ci.PodIDKey] = pod.PodRef.UID
		tags[ci.K8sPodNameKey] = pod.PodRef.Name
		tags[ci.K8sNamespace] = pod.PodRef.Namespace
		tags[ci.Timestamp] = strconv.FormatInt(pod.CPU.Time.UnixNano(), 10)
		rawMetric := extractors.ConvertPodToRaw(&pod)
		for _, extractor := range GetMetricsExtractors() {
			if extractor.HasValue(rawMetric) {
				metricsPerPod = append(metricsPerPod, extractor.GetValue(rawMetric, k.hostInfo, ci.TypePod)...)
			}
		}
		for _, metric := range metricsPerPod {
			metric.AddTags(tags)
		}
		metrics = append(metrics, metricsPerPod...)

		containerMetrics, err := k.getContainerMetrics(&pod)
		if err != nil {
			k.logger.Error("kubelet container metrics failed, ", zap.Error(err))
			return containerMetrics, err
		}
		metrics = append(metrics, containerMetrics...)
	}
	return metrics, nil
}

// getNodeMetrics returns Node level metrics from kubelet.
func (k *kubelet) getNodeMetrics(summary *stats.Summary) ([]*cExtractor.CAdvisorMetric, error) {
	var metrics []*cExtractor.CAdvisorMetric

	rawMetric := extractors.ConvertNodeToRaw(&summary.Node)
	for _, extractor := range GetMetricsExtractors() {
		if extractor.HasValue(rawMetric) {
			metrics = append(metrics, extractor.GetValue(rawMetric, k.hostInfo, ci.TypeNode)...)
		}
	}
	return metrics, nil
}
