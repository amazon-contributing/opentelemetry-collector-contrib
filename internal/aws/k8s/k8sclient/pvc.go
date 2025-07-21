// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type PVCClient interface {
	// NamespaceToPVCCount returns a map of namespace to PVC count
	NamespaceToPVCCount() map[string]int
	// TotalPVCCount returns the total number of PVCs in the cluster
	TotalPVCCount() int
}

type noOpPVCClient struct{}

func (p *noOpPVCClient) NamespaceToPVCCount() map[string]int {
	return map[string]int{}
}

func (p *noOpPVCClient) TotalPVCCount() int {
	return 0
}

func (p *noOpPVCClient) shutdown() {
}

type pvcClientOption func(*pvcClient)

func pvcSyncCheckerOption(checker initialSyncChecker) pvcClientOption {
	return func(p *pvcClient) {
		p.syncChecker = checker
	}
}

type pvcClient struct {
	stopChan chan struct{}
	stopped  bool

	store *ObjStore

	syncChecker initialSyncChecker

	mu                sync.RWMutex
	namespaceToPVCMap map[string]int
	totalCount        int
}

func (p *pvcClient) refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()

	namespaceToPVCMap := make(map[string]int)
	totalCount := 0

	objsList := p.store.List()
	for _, obj := range objsList {
		pvc, ok := obj.(*corev1.PersistentVolumeClaim)
		if !ok {
			continue
		}
		namespaceToPVCMap[pvc.Namespace]++
		totalCount++
	}

	p.namespaceToPVCMap = namespaceToPVCMap
	p.totalCount = totalCount
}

func (p *pvcClient) NamespaceToPVCCount() map[string]int {
	if p.store.GetResetRefreshStatus() {
		p.refresh()
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]int)
	for ns, count := range p.namespaceToPVCMap {
		result[ns] = count
	}
	return result
}

func (p *pvcClient) TotalPVCCount() int {
	if p.store.GetResetRefreshStatus() {
		p.refresh()
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.totalCount
}

func newPVCClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...pvcClientOption) (*pvcClient, error) {
	p := &pvcClient{
		stopChan:          make(chan struct{}),
		namespaceToPVCMap: make(map[string]int),
	}

	for _, option := range options {
		option(p)
	}

	ctx := context.Background()
	if _, err := clientSet.CoreV1().PersistentVolumeClaims(metav1.NamespaceAll).List(ctx, metav1.ListOptions{}); err != nil {
		return nil, fmt.Errorf("cannot list PVCs. err: %w", err)
	}

	// Create a store to hold PVC objects
	p.store = NewObjStore(transformFuncPVC, logger)
	// Create a ListWatch that knows how to list and watch PVCs
	lw := createPVCListWatch(clientSet, metav1.NamespaceAll)
	// Create a Reflector that watches PVCs and updates the store
	reflector := cache.NewReflector(lw, &corev1.PersistentVolumeClaim{}, p.store, 0)
	// Start the Reflector in a goroutine
	go reflector.Run(p.stopChan)

	if p.syncChecker != nil {
		// Check the init sync for potential connection issue
		p.syncChecker.Check(reflector, "PVC initial sync timeout")
	}

	return p, nil
}

func (p *pvcClient) shutdown() {
	close(p.stopChan)
	p.stopped = true
}

func transformFuncPVC(obj interface{}) (interface{}, error) {
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not PersistentVolumeClaim type", obj)
	}
	return pvc, nil
}

func createPVCListWatch(client kubernetes.Interface, ns string) cache.ListerWatcher {
	ctx := context.Background()
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().PersistentVolumeClaims(ns).List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().PersistentVolumeClaims(ns).Watch(ctx, opts)
		},
	}
}
