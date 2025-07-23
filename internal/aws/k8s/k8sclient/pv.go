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

type PVClient interface {
	Client
}

type pvClient struct {
	*BaseResourceClient[*corev1.PersistentVolume]
}

type noOpPVClient struct {
	noOpClient
}

// PV-specific functions
func refreshFuncPV(objs []*corev1.PersistentVolume) (int, map[string]int) {
	totalCount := 0
	for _, pv := range objs {
		if pv == nil {
			continue
		}
		totalCount++
	}
	return totalCount, map[string]int{} // PVs are not namespaced, so we return an empty map
}

func transformFuncPV(obj interface{}) (*corev1.PersistentVolume, error) {
	pv, ok := obj.(*corev1.PersistentVolume)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not PersistentVolume type", obj)
	}
	return pv, nil
}

func createPVListWatch(client kubernetes.Interface, ns string) cache.ListerWatcher {
	ctx := context.Background()
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			return client.CoreV1().PersistentVolumes().List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			return client.CoreV1().PersistentVolumes().Watch(ctx, opts)
		},
	}
}

func testPVList(clientSet kubernetes.Interface) error {
	ctx := context.Background()
	_, err := clientSet.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	return err
}

func newPVObject() *corev1.PersistentVolume {
	return &corev1.PersistentVolume{}
}

func NewPVClient(clientSet kubernetes.Interface, logger *zap.Logger, syncChecker initialSyncChecker) (*pvClient, error) {
	config := ResourceConfig[*corev1.PersistentVolume]{
		ResourceName:  "PV",
		ListWatchFunc: createPVListWatch,
		TransformFunc: transformFuncPV,
		RefreshFunc:   refreshFuncPV,
		TestListFunc:  testPVList,
		NewObjectFunc: newPVObject,
		Namespace:     "", // PVs are cluster-scoped
	}

	base, err := NewBaseResourceClient(clientSet, logger, config, syncChecker)
	if err != nil {
		return nil, err
	}

	return &pvClient{base}, nil
}
