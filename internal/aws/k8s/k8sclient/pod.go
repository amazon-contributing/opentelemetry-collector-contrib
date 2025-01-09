// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type PodClient interface {
	// Get the mapping between the namespace and the number of belonging pods
	NamespaceToRunningPodNum() map[string]int
	PodInfos() []*PodInfo
}

type podClientOption func(*podClient)

func podSyncCheckerOption(checker initialSyncChecker) podClientOption {
	return func(p *podClient) {
		p.syncChecker = checker
	}
}

type podClient struct {
	stopChan chan struct{}
	store    *ObjStore
	informer cache.SharedIndexInformer

	stopped     bool
	syncChecker initialSyncChecker

	mu                          sync.RWMutex
	namespaceToRunningPodNumMap map[string]int
	podInfos                    []*PodInfo
	logger                      *zap.Logger
}

func (c *podClient) NamespaceToRunningPodNum() map[string]int {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.namespaceToRunningPodNumMap
}

func (c *podClient) PodInfos() []*PodInfo {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.podInfos
}

func (c *podClient) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()

	objsList := c.store.List()
	namespaceToRunningPodNumMapNew := make(map[string]int)
	podInfos := make([]*PodInfo, 0)
	for _, obj := range objsList {
		pod := obj.(*PodInfo)
		podInfos = append(podInfos, pod)

		if pod.Phase == v1.PodRunning {
			if podNum, ok := namespaceToRunningPodNumMapNew[pod.Namespace]; !ok {
				namespaceToRunningPodNumMapNew[pod.Namespace] = 1
			} else {
				namespaceToRunningPodNumMapNew[pod.Namespace] = podNum + 1
			}
		}
	}
	c.podInfos = podInfos
	c.namespaceToRunningPodNumMap = namespaceToRunningPodNumMapNew
}

func newPodClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...podClientOption) *podClient {
	c := &podClient{
		stopChan: make(chan struct{}),
	}

	for _, option := range options {
		option(c)
	}

	c.store = NewObjStore(transformFuncPod, logger)
	c.logger = logger

	var err error
	c.informer, err = createSharedPodsInformer(clientSet, c.store)
	if err != nil {
		c.logger.Warn("Failed to create Pod informer", zap.Error(err))
		return nil
	}
	go c.informer.Run(c.stopChan)

	if c.syncChecker != nil {
		if c.syncChecker.Check(c.informer, "Pod initial sync timeout") {
			if !cache.WaitForCacheSync(c.stopChan, c.informer.HasSynced) {
				c.logger.Warn("Pod informer cache sync timeout")
			}
		}
	}

	return c
}

func (c *podClient) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	close(c.stopChan)
	c.stopped = true
}

func transformFuncPod(obj any) (any, error) {
	pod, ok := obj.(*v1.Pod)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not Pod type", obj)
	}
	info := new(PodInfo)
	info.Name = pod.Name
	info.Namespace = pod.Namespace
	info.UID = string(pod.UID)
	info.Labels = pod.Labels
	info.OwnerReferences = pod.OwnerReferences
	info.Phase = pod.Status.Phase
	info.Conditions = pod.Status.Conditions
	return info, nil
}

// This function removes all data from the Pod except what is required
func removeUnnecessaryPodData(pod *v1.Pod) *v1.Pod {

	// name, namespace, uid, start time and ip are needed for identifying Pods
	// there's room to optimize this further, it's kept this way for simplicity
	transformedPod := v1.Pod{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:            pod.GetName(),
			Namespace:       pod.GetNamespace(),
			UID:             pod.GetUID(),
			Labels:          pod.GetLabels(),
			OwnerReferences: pod.OwnerReferences,
		},
		Status: v1.PodStatus{
			PodIP:      pod.Status.PodIP,
			StartTime:  pod.Status.StartTime,
			Phase:      pod.Status.Phase,
			Conditions: pod.Status.Conditions,
		},
	}

	return &transformedPod
}

// createSharedPodsInformer creates a shared informer for pods
func createSharedPodsInformer(clientSet kubernetes.Interface, store *ObjStore) (cache.SharedIndexInformer, error) {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Core().V1().Pods().Informer()
	err := sharedIndexInformer.SetTransform(
		func(object any) (any, error) {
			originalPod, success := object.(*v1.Pod)
			if !success { // means this is a cache.DeletedFinalStateUnknown, in which case we do nothing
				return object, nil
			}

			return removeUnnecessaryPodData(originalPod), nil
		},
	)
	if err != nil {
		return nil, err
	}

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
	return sharedIndexInformer, nil
}
