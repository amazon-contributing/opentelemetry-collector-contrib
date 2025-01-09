// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type StatefulSetClient interface {
	// StatefulSetInfos contains the information about each statefulSet in the cluster
	StatefulSetInfos() []*StatefulSetInfo
}

type noOpStatefulSetClient struct {
}

func (nd *noOpStatefulSetClient) StatefulSetInfos() []*StatefulSetInfo {
	return []*StatefulSetInfo{}
}

func (nd *noOpStatefulSetClient) shutdown() {
}

type statefulSetClientOption func(*statefulSetClient)

func statefulSetSyncCheckerOption(checker initialSyncChecker) statefulSetClientOption {
	return func(d *statefulSetClient) {
		d.syncChecker = checker
	}
}

type statefulSetClient struct {
	stopChan chan struct{}
	stopped  bool

	store    *ObjStore
	informer cache.SharedIndexInformer

	syncChecker initialSyncChecker

	mu               sync.RWMutex
	statefulSetInfos []*StatefulSetInfo
	logger           *zap.Logger
}

func (d *statefulSetClient) refresh() {
	d.mu.Lock()
	defer d.mu.Unlock()

	var statefulSetInfos []*StatefulSetInfo
	objsList := d.store.List()
	for _, obj := range objsList {
		statefulSet, ok := obj.(*StatefulSetInfo)
		if !ok {
			continue
		}
		statefulSetInfos = append(statefulSetInfos, statefulSet)
	}

	d.statefulSetInfos = statefulSetInfos
}

func (d *statefulSetClient) StatefulSetInfos() []*StatefulSetInfo {
	if d.store.GetResetRefreshStatus() {
		d.refresh()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.statefulSetInfos
}

func newStatefulSetClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...statefulSetClientOption) (*statefulSetClient, error) {
	d := &statefulSetClient{
		stopChan: make(chan struct{}),
	}

	for _, option := range options {
		option(d)
	}

	d.store = NewObjStore(transformFuncStatefulSet, logger)
	d.logger = logger

	d.informer = createSharedStatefulsetsInformer(clientSet, d.store)
	go d.informer.Run(d.stopChan)

	if d.syncChecker != nil {
		if d.syncChecker.Check(d.informer, "StatefulSet initial sync timeout") {
			if !cache.WaitForCacheSync(d.stopChan, d.informer.HasSynced) {
				d.logger.Warn("StatefulSet informer cache sync timeout")
			}
		}
	}

	return d, nil
}

func (d *statefulSetClient) shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	close(d.stopChan)
	d.stopped = true
}

func transformFuncStatefulSet(obj any) (any, error) {
	statefulSet, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not StatefulSet type", obj)
	}
	info := new(StatefulSetInfo)
	info.Name = statefulSet.Name
	info.Namespace = statefulSet.Namespace
	info.Spec = &StatefulSetSpec{
		Replicas: uint32(*statefulSet.Spec.Replicas),
	}
	info.Status = &StatefulSetStatus{
		Replicas:          uint32(statefulSet.Status.Replicas),
		AvailableReplicas: uint32(statefulSet.Status.AvailableReplicas),
		ReadyReplicas:     uint32(statefulSet.Status.ReadyReplicas),
	}
	return info, nil
}

// createSharedStatefulsetsInformer creates a shared informer for statefulsets
func createSharedStatefulsetsInformer(clientSet kubernetes.Interface, store *ObjStore) cache.SharedIndexInformer {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Apps().V1().StatefulSets().Informer()

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
