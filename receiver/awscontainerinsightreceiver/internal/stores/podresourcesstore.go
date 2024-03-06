// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package stores // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores"

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	v1 "k8s.io/kubelet/pkg/apis/podresources/v1"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/stores/kubeletutil"
)

const (
	taskTimeout = 10 * time.Second
)

var (
	instance *PodResourcesStore
	once     sync.Once
)

type ContainerInfo struct {
	PodName       string
	ContainerName string
	Namespace     string
}

type ResourceInfo struct {
	resourceName string
	deviceID     string
}

type PodResourcesClientInterface interface {
	ListPods() (*v1.ListPodResourcesResponse, error)
	Shutdown()
}

type PodResourcesStore struct {
	containerInfoToResourcesMap map[ContainerInfo][]ResourceInfo
	resourceToPodContainerMap   map[ResourceInfo]ContainerInfo
	resourceNameSet             map[string]struct{}
	lastRefreshed               time.Time
	ctx                         context.Context
	cancel                      context.CancelFunc
	logger                      *zap.Logger
	podResourcesClient          PodResourcesClientInterface
}

func NewPodResourcesStore(logger *zap.Logger) *PodResourcesStore {
	once.Do(func() {
		podResourcesClient, _ := kubeletutil.NewPodResourcesClient(logger)
		ctx, cancel := context.WithCancel(context.Background())
		instance = &PodResourcesStore{
			containerInfoToResourcesMap: make(map[ContainerInfo][]ResourceInfo),
			resourceToPodContainerMap:   make(map[ResourceInfo]ContainerInfo),
			resourceNameSet:             make(map[string]struct{}),
			lastRefreshed:               time.Now(),
			ctx:                         ctx,
			cancel:                      cancel,
			logger:                      logger,
			podResourcesClient:          podResourcesClient,
		}

		go func() {
			refreshTicker := time.NewTicker(time.Second)
			for {
				select {
				case <-refreshTicker.C:
					logger.Info("entered refresh tick")
					instance.refreshTick()
				case <-instance.ctx.Done():
					logger.Info("stopping refresh tick")
					refreshTicker.Stop()
					return
				}
			}
		}()
		time.Sleep(40)
	})
	return instance
}

func (p *PodResourcesStore) refreshTick() {
	p.logger.Info("PodResources entered refreshTick")
	now := time.Now()
	if now.Sub(p.lastRefreshed) >= taskTimeout {
		p.refresh()
		p.lastRefreshed = now
	}
}

func (p *PodResourcesStore) refresh() {
	p.logger.Info("PodResources entered refresh")
	doRefresh := func() {
		p.logger.Info("PodResources entering update map")
		p.updateMaps()
	}

	refreshWithTimeout(p.ctx, doRefresh, taskTimeout)
}

func (p *PodResourcesStore) updateMaps() {
	p.containerInfoToResourcesMap = make(map[ContainerInfo][]ResourceInfo)
	p.resourceToPodContainerMap = make(map[ResourceInfo]ContainerInfo)

	if len(p.resourceNameSet) == 0 {
		p.logger.Warn("No resource names allowlisted thus skipping updating of maps.")
		return
	}

	devicePods, err := p.podResourcesClient.ListPods()
	if err != nil {
		p.logger.Info("PodResources ListPods calling error: " + err.Error())
	}
	if err != nil {
		p.logger.Error(fmt.Sprintf("Error getting pod resources: %v", err))
		return
	}

	p.logger.Info("PodResources updating device info with result : " + devicePods.String())
	for _, pod := range devicePods.GetPodResources() {
		for _, container := range pod.GetContainers() {
			for _, device := range container.GetDevices() {

				containerInfo := ContainerInfo{
					PodName:       pod.GetName(),
					Namespace:     pod.GetNamespace(),
					ContainerName: container.GetName(),
				}

				for _, deviceID := range device.GetDeviceIds() {
					resourceInfo := ResourceInfo{
						resourceName: device.GetResourceName(),
						deviceID:     deviceID,
					}
					_, found := p.resourceNameSet[resourceInfo.resourceName]
					if found {
						p.containerInfoToResourcesMap[containerInfo] = append(p.containerInfoToResourcesMap[containerInfo], resourceInfo)
						p.resourceToPodContainerMap[resourceInfo] = containerInfo

						p.logger.Info("/nContainerInfo : {" + containerInfo.Namespace + "_" + containerInfo.PodName + "_" + containerInfo.ContainerName + "}" + " -> ResourceInfo : {" + resourceInfo.resourceName + "_" + resourceInfo.deviceID + "_" + "}")
					}
				}
			}
		}
	}
}

func (p *PodResourcesStore) GetContainerInfo(deviceID string, resourceName string) *ContainerInfo {
	key := ResourceInfo{deviceID: deviceID, resourceName: resourceName}
	if containerInfo, ok := p.resourceToPodContainerMap[key]; ok {
		return &containerInfo
	}
	return nil
}

func (p *PodResourcesStore) GetResourcesInfo(podName string, containerName string, namespace string) *[]ResourceInfo {
	key := ContainerInfo{PodName: podName, ContainerName: containerName, Namespace: namespace}
	if resourceInfo, ok := p.containerInfoToResourcesMap[key]; ok {
		return &resourceInfo
	}
	return nil
}

func (p *PodResourcesStore) AddResourceName(resourceName string) {
	p.resourceNameSet[resourceName] = struct{}{}
}

func (p *PodResourcesStore) UpdateAndPrintMapsManually() {
	// this also has embedded print statement
	p.updateMaps()
}

func (p *PodResourcesStore) Shutdown() {
	p.cancel()
	p.podResourcesClient.Shutdown()
}
