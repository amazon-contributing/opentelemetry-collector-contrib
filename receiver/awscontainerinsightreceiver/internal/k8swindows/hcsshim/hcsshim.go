// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build windows
// +build windows

package hcsshim // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/hcsshim"

import (
	"fmt"
	"strconv"
	"strings"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
	cExtractor "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/cadvisor/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/extractors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/k8swindows/kubelet"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"

	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

type PodKey struct {
	PodId, PodName, PodNamespace string
	Containers                   []ContainerInfo
}

type ContainerInfo struct {
	Name, Id string
}

type EndpointInfo struct {
	Id, Name string
}

type HCSStatsProvider struct {
	logger              *zap.Logger
	hostInfo            cExtractor.CPUMemInfoProvider
	hcsClient           HCSClient
	kubeletProvider     kubelet.KubeletProvider
	metricExtractors    []extractors.MetricExtractor
	containerToEndpoint map[string]EndpointInfo
}

func createHCSClient(logger *zap.Logger) HCSClient {
	return &hCSClient{logger: logger}
}

// Options decorates SummaryProvider struct.
type Options func(provider *HCSStatsProvider)

func NewHnSProvider(logger *zap.Logger, info cExtractor.CPUMemInfoProvider, mextractor []extractors.MetricExtractor, opts ...Options) (*HCSStatsProvider, error) {
	hp := &HCSStatsProvider{
		logger:           logger,
		hostInfo:         info,
		kubeletProvider:  kubelet.CreateDefaultKubeletProvider(logger),
		hcsClient:        createHCSClient(logger),
		metricExtractors: mextractor,
	}

	for _, opt := range opts {
		opt(hp)
	}

	return hp, nil
}

func (hp *HCSStatsProvider) GetMetrics() ([]*stores.CIMetricImpl, error) {
	var metrics []*stores.CIMetricImpl
	if ci.IsWindowsHostProcessContainer() {
		containerToEndpointMap, err := hp.getContainerToEndpointMap()
		if err != nil {
			hp.logger.Error("failed to create container to endpoint map using HCS shim APIs, ", zap.Error(err))
			return nil, err
		}
		hp.containerToEndpoint = containerToEndpointMap
		return hp.getPodMetrics()
	}
	return metrics, nil
}

func (hp *HCSStatsProvider) getContainerMetrics(containerId string) (extractors.HCSStat, error) {
	hp.logger.Debug("Getting Container stats using Microsoft HCS shim APIs")

	cps, err := hp.hcsClient.GetContainerStats(containerId)
	if err != nil {
		hp.logger.Error("failed to get container stats from HCS shim client, ", zap.Error(err))
		return extractors.HCSStat{}, err
	}

	if _, ok := hp.containerToEndpoint[containerId]; !ok {
		hp.logger.Warn("HNS endpoint not found ", zap.String("container", containerId))
		return extractors.HCSStat{}, nil
	}

	endpoint := hp.containerToEndpoint[containerId]
	enpointStat, err := hp.hcsClient.GetEndpointStat(endpoint.Id)
	if err != nil {
		hp.logger.Error("failed to get HNS endpoint stats, ", zap.Error(err))
		return extractors.HCSStat{}, err
	}
	var hnsNetworks []extractors.HCSNetworkStat

	hnsNetworks = append(hnsNetworks, extractors.HCSNetworkStat{
		Name:                   endpoint.Name,
		BytesReceived:          enpointStat.BytesReceived,
		BytesSent:              enpointStat.BytesSent,
		DroppedPacketsOutgoing: enpointStat.DroppedPacketsOutgoing,
		DroppedPacketsIncoming: enpointStat.DroppedPacketsIncoming,
	})

	stat := extractors.HCSStat{Time: cps.Timestamp, CPU: &cps.Processor, Network: &hnsNetworks}
	hp.logger.Debug("Returning Container stats using Microsoft HCS shim APIs")
	return stat, nil
}

func (hp *HCSStatsProvider) getPodMetrics() ([]*stores.CIMetricImpl, error) {
	hp.logger.Debug("Getting pod stats using Microsoft HCS shim APIs")
	podToContainerMap, err := hp.getPodToContainerMap()
	if err != nil {
		hp.logger.Error("failed to create pod to container map using kubelet APIs, ", zap.Error(err))
		return nil, err
	}

	var metrics []*stores.CIMetricImpl
	var endpointMetricsCollected []string

	for _, pod := range podToContainerMap {
		var metricsPerPod []*stores.CIMetricImpl
		tags := map[string]string{}

		tags[ci.AttributePodID] = pod.PodId
		tags[ci.AttributeK8sPodName] = pod.PodName
		tags[ci.AttributeK8sNamespace] = pod.PodNamespace

		for _, container := range pod.Containers {
			if _, ok := hp.containerToEndpoint[container.Id]; !ok {
				hp.logger.Debug("Skipping as endpoint don't exist for container")
				continue
			}
			endpoint := hp.containerToEndpoint[container.Id]
			if slices.Contains(endpointMetricsCollected, endpoint.Id) {
				hp.logger.Debug("Skipping as metric already collected for HNS Endpoint")
				continue
			}

			containerStats, err := hp.getContainerMetrics(container.Id)
			if err != nil {
				hp.logger.Warn("failed to get container metrics using HCS shim APIs, ", zap.Error(err))
				continue
			}

			rawMetric := extractors.ConvertHCSContainerToRaw(containerStats)
			tags[ci.Timestamp] = strconv.FormatInt(rawMetric.Time.UnixNano(), 10)

			for _, extractor := range hp.metricExtractors {
				if extractor.HasValue(rawMetric) {
					metricsPerPod = append(metricsPerPod, extractor.GetValue(rawMetric, hp.hostInfo, ci.TypePod)...)
				}
			}
			endpointMetricsCollected = append(endpointMetricsCollected, hp.containerToEndpoint[container.Id].Id)
		}
		for _, metric := range metricsPerPod {
			metric.AddTags(tags)
		}
		metrics = append(metrics, metricsPerPod...)
	}

	return metrics, nil
}

func (hp *HCSStatsProvider) getPodToContainerMap() (map[string]PodKey, error) {
	containerNameToIdMapping := make(map[string]PodKey)
	podList, err := hp.kubeletProvider.GetPods()
	if err != nil {
		hp.logger.Error("failed to get pod list from kubelet provider, ", zap.Error(err))
		return nil, err
	}
	for _, pod := range podList {
		podId := fmt.Sprintf("%s", pod.UID)
		podKey := PodKey{
			PodId:        podId,
			PodName:      pod.Name,
			PodNamespace: pod.Namespace,
		}
		if _, ok := containerNameToIdMapping[podId]; !ok {
			containerNameToIdMapping[podId] = podKey
		}

		for _, container := range pod.Status.ContainerStatuses {
			if strings.Contains(container.ContainerID, "containerd") {
				cinfo := ContainerInfo{
					Id:   strings.Split(container.ContainerID, "containerd://")[1],
					Name: container.Name,
				}
				podKey.Containers = append(podKey.Containers, cinfo)
			}
		}
		containerNameToIdMapping[podId] = podKey
	}

	return containerNameToIdMapping, nil
}

func (hp *HCSStatsProvider) getContainerToEndpointMap() (map[string]EndpointInfo, error) {
	var containerToEndpointMap = make(map[string]EndpointInfo)
	endpointList, err := hp.hcsClient.GetEndpointList()
	if err != nil {
		hp.logger.Error("failed to get endpoints list from HCS shim client, ", zap.Error(err))
		return containerToEndpointMap, err
	}

	for _, endpoint := range endpointList {
		for _, container := range endpoint.SharedContainers {
			containerToEndpointMap[container] = EndpointInfo{Id: endpoint.Id, Name: endpoint.Name}
		}
	}

	return containerToEndpointMap, nil
}
