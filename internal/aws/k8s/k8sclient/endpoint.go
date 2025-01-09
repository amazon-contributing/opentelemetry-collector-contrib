// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sutil"
)

const (
	typePod = "Pod"
)

type Service struct {
	ServiceName string
	Namespace   string
}

func NewService(name, namespace string) Service {
	return Service{ServiceName: name, Namespace: namespace}
}

type EpClient interface {
	// Get the mapping between pod key and the corresponding service names
	PodKeyToServiceNames() map[string][]string
	// Get the mapping between the service and the number of belonging pods
	ServiceToPodNum() map[Service]int
}

type epClientOption func(*epClient)

func epSyncCheckerOption(checker initialSyncChecker) epClientOption {
	return func(e *epClient) {
		e.syncChecker = checker
	}
}

type epClient struct {
	stopChan chan struct{}
	store    *ObjStore
	informer cache.SharedIndexInformer

	stopped bool

	syncChecker initialSyncChecker

	mu                      sync.RWMutex
	podKeyToServiceNamesMap map[string][]string
	serviceToPodNumMap      map[Service]int // only running pods will show behind endpoints
	logger                  *zap.Logger
}

func (c *epClient) PodKeyToServiceNames() map[string][]string {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.podKeyToServiceNamesMap
}

func (c *epClient) ServiceToPodNum() map[Service]int {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serviceToPodNumMap
}

func (c *epClient) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()

	objsList := c.store.List()

	tmpMap := make(map[string]map[string]struct{}) // pod key to service names
	serviceToPodNumMapNew := make(map[Service]int)

	for _, obj := range objsList {
		ep := obj.(*endpointInfo)
		serviceName := ep.name
		namespace := ep.namespace

		// each obj should be a uniq service.
		// ignore the service which has 0 pods.
		if len(ep.podKeyList) > 0 {
			serviceToPodNumMapNew[NewService(serviceName, namespace)] = len(ep.podKeyList)
		}

		for _, podKey := range ep.podKeyList {
			var serviceNamesMap map[string]struct{}
			var ok bool
			if _, ok = tmpMap[podKey]; !ok {
				tmpMap[podKey] = make(map[string]struct{})
			}
			serviceNamesMap = tmpMap[podKey]
			serviceNamesMap[serviceName] = struct{}{}
		}
	}

	podKeyToServiceNamesMapNew := make(map[string][]string)

	for podKey, serviceNamesMap := range tmpMap {
		serviceNamesList := make([]string, 0, len(serviceNamesMap))
		for serviceName := range serviceNamesMap {
			serviceNamesList = append(serviceNamesList, serviceName)
		}
		podKeyToServiceNamesMapNew[podKey] = serviceNamesList
	}
	c.podKeyToServiceNamesMap = podKeyToServiceNamesMapNew
	c.serviceToPodNumMap = serviceToPodNumMapNew
}

func newEpClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...epClientOption) *epClient {
	c := &epClient{
		stopChan: make(chan struct{}),
	}

	for _, option := range options {
		option(c)
	}

	c.store = NewObjStore(transformFuncEndpoint, logger)
	c.logger = logger

	c.informer = createSharedEndpointsInformer(clientSet, c.store)
	go c.informer.Run(c.stopChan)

	if c.syncChecker != nil {
		if c.syncChecker.Check(c.informer, "Endpoint initial sync timeout") {
			if !cache.WaitForCacheSync(c.stopChan, c.informer.HasSynced) {
				c.logger.Warn("Endpoint informer cache sync timeout")
			}
		}
	}

	return c
}

func (c *epClient) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.stopChan)
	c.stopped = true
}

func transformFuncEndpoint(obj any) (any, error) {
	endpoint, ok := obj.(*v1.Endpoints)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not Endpoint type", obj)
	}
	info := new(endpointInfo)
	info.name = endpoint.Name
	info.namespace = endpoint.Namespace
	info.podKeyList = []string{}
	if subsets := endpoint.Subsets; subsets != nil {
		for _, subset := range subsets {
			if addresses := subset.Addresses; addresses != nil {
				for _, address := range addresses {
					if targetRef := address.TargetRef; targetRef != nil && targetRef.Kind == typePod {
						podKey := k8sutil.CreatePodKey(targetRef.Namespace, targetRef.Name)
						if podKey == "" {
							continue
						}
						info.podKeyList = append(info.podKeyList, podKey)
					}
				}
			}
		}
	}
	return info, nil
}

// createSharedEndpointsInformer creates a shared informer for endpoints
func createSharedEndpointsInformer(clientSet kubernetes.Interface, store *ObjStore) cache.SharedIndexInformer {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Core().V1().Endpoints().Informer()

	sharedIndexInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			store.Add(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			store.Update(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			store.Delete(obj)
		},
	})
	return sharedIndexInformer
}
