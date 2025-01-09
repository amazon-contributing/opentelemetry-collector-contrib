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

type DaemonSetClient interface {
	// DaemonSetInfos contains the information about each daemon set in the cluster
	DaemonSetInfos() []*DaemonSetInfo
}

type noOpDaemonSetClient struct {
}

func (nd *noOpDaemonSetClient) DaemonSetInfos() []*DaemonSetInfo {
	return []*DaemonSetInfo{}
}

func (nd *noOpDaemonSetClient) shutdown() {
}

type daemonSetClientOption func(*daemonSetClient)

func daemonSetSyncCheckerOption(checker initialSyncChecker) daemonSetClientOption {
	return func(d *daemonSetClient) {
		d.syncChecker = checker
	}
}

type daemonSetClient struct {
	stopChan chan struct{}
	stopped  bool

	store    *ObjStore
	informer cache.SharedIndexInformer

	syncChecker initialSyncChecker

	mu             sync.RWMutex
	daemonSetInfos []*DaemonSetInfo
	logger         *zap.Logger
}

func (d *daemonSetClient) refresh() {
	d.mu.Lock()
	defer d.mu.Unlock()

	var daemonSetInfos []*DaemonSetInfo
	objsList := d.store.List()
	for _, obj := range objsList {
		daemonSet, ok := obj.(*DaemonSetInfo)
		if !ok {
			continue
		}
		daemonSetInfos = append(daemonSetInfos, daemonSet)
	}

	d.daemonSetInfos = daemonSetInfos
}

func (d *daemonSetClient) DaemonSetInfos() []*DaemonSetInfo {
	if d.store.GetResetRefreshStatus() {
		d.refresh()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.daemonSetInfos
}

func newDaemonSetClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...daemonSetClientOption) (*daemonSetClient, error) {
	d := &daemonSetClient{
		stopChan: make(chan struct{}),
	}

	for _, option := range options {
		option(d)
	}

	d.store = NewObjStore(transformFuncDaemonSet, logger)
	d.logger = logger

	d.informer = createSharedDaemonsetsInformer(clientSet, d.store)
	go d.informer.Run(d.stopChan)

	if d.syncChecker != nil {
		if d.syncChecker.Check(d.informer, "DaemonSet initial sync timeout") {
			if !cache.WaitForCacheSync(d.stopChan, d.informer.HasSynced) {
				d.logger.Warn("Daemonset informer cache sync timeout")
			}
		}
	}

	return d, nil
}

func (d *daemonSetClient) shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	close(d.stopChan)
	d.stopped = true
}

func transformFuncDaemonSet(obj any) (any, error) {
	daemonSet, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not DaemonSet type", obj)
	}
	info := new(DaemonSetInfo)
	info.Name = daemonSet.Name
	info.Namespace = daemonSet.Namespace
	info.Status = &DaemonSetStatus{
		NumberAvailable:        uint32(daemonSet.Status.NumberAvailable),
		NumberUnavailable:      uint32(daemonSet.Status.NumberUnavailable),
		DesiredNumberScheduled: uint32(daemonSet.Status.DesiredNumberScheduled),
		CurrentNumberScheduled: uint32(daemonSet.Status.CurrentNumberScheduled),
	}
	return info, nil
}

// createSharedDaemonsetsInformer creates a shared informer for daemonsets
func createSharedDaemonsetsInformer(clientSet kubernetes.Interface, store *ObjStore) cache.SharedIndexInformer {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Apps().V1().DaemonSets().Informer()

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
