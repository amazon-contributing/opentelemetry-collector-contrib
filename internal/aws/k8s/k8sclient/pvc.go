// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type PVCClient interface {
	CountByNamespaceClient
}

type pvcClient struct {
	*BaseResourceClient[*corev1.PersistentVolumeClaim]
}

type noOpPVCClient struct {
	noOpClient
}

func (p *noOpPVCClient) CountByNamespace() map[string]int { return map[string]int{} }

func (p *pvcClient) CountByNamespace() map[string]int {
	return p.GetExtraMap()
}

// PVC-specific functions
func refreshFuncPVC(objs []*corev1.PersistentVolumeClaim) (int, map[string]int) {
	namespaceToPVCMap := make(map[string]int)
	totalCount := 0

	for _, pvc := range objs {
		if pvc == nil {
			continue
		}
		namespaceToPVCMap[pvc.Namespace]++
		totalCount++
	}

	return totalCount, namespaceToPVCMap
}

func transformFuncPVC(obj interface{}) (*corev1.PersistentVolumeClaim, error) {
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

func testPVCList(clientSet kubernetes.Interface) error {
	ctx := context.Background()
	_, err := clientSet.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	return err
}

func newPVCObject() *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{}
}

func NewPVCClient(clientSet kubernetes.Interface, logger *zap.Logger, syncChecker initialSyncChecker) (*pvcClient, error) {
	config := ResourceConfig[*corev1.PersistentVolumeClaim]{
		ResourceName:  "PVC",
		ListWatchFunc: createPVCListWatch,
		TransformFunc: transformFuncPVC,
		RefreshFunc:   refreshFuncPVC,
		TestListFunc:  testPVCList,
		NewObjectFunc: newPVCObject,
		Namespace:     metav1.NamespaceAll,
	}

	base, err := NewBaseResourceClient(clientSet, logger, config, syncChecker)
	if err != nil {
		return nil, err
	}

	return &pvcClient{base}, nil
}
