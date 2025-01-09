// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	deployment = "Deployment"
)

type ReplicaSetClient interface {
	// Get the mapping between replica set and deployment
	ReplicaSetToDeployment() map[string]string
	ReplicaSetInfos() []*ReplicaSetInfo
}

type noOpReplicaSetClient struct {
}

func (nc *noOpReplicaSetClient) ReplicaSetToDeployment() map[string]string {
	return map[string]string{}
}

func (nc *noOpReplicaSetClient) ReplicaSetInfos() []*ReplicaSetInfo {
	return []*ReplicaSetInfo{}
}

func (nc *noOpReplicaSetClient) shutdown() {
}

type replicaSetClientOption func(*replicaSetClient)

func replicaSetSyncCheckerOption(checker initialSyncChecker) replicaSetClientOption {
	return func(r *replicaSetClient) {
		r.syncChecker = checker
	}
}

type replicaSetClient struct {
	stopChan chan struct{}
	store    *ObjStore
	informer cache.SharedIndexInformer

	stopped     bool
	syncChecker initialSyncChecker

	mu                        sync.RWMutex
	cachedReplicaSetMap       map[string]time.Time
	replicaSetToDeploymentMap map[string]string
	replicaSetInfos           []*ReplicaSetInfo
	logger                    *zap.Logger
}

func (c *replicaSetClient) ReplicaSetToDeployment() map[string]string {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.replicaSetToDeploymentMap
}

func (c *replicaSetClient) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()

	objsList := c.store.List()

	var replicaSetInfos []*ReplicaSetInfo
	tmpMap := make(map[string]string)
	for _, obj := range objsList {
		replicaSet := obj.(*ReplicaSetInfo)
		if len(replicaSet.Owners) > 0 {
		ownerLoop:
			for _, owner := range replicaSet.Owners {
				if owner.kind == deployment && owner.name != "" {
					tmpMap[replicaSet.Name] = owner.name
					break ownerLoop
				}
			}
		} else {
			// replicaSet without owner reference is not part of a deployment
			replicaSetInfos = append(replicaSetInfos, replicaSet)
		}
	}

	lastRefreshTime := time.Now()

	for k, v := range c.cachedReplicaSetMap {
		if lastRefreshTime.Sub(v) > cacheTTL {
			delete(c.replicaSetToDeploymentMap, k)
			delete(c.cachedReplicaSetMap, k)
		}
	}

	for k, v := range tmpMap {
		c.replicaSetToDeploymentMap[k] = v
		c.cachedReplicaSetMap[k] = lastRefreshTime
	}

	c.replicaSetInfos = replicaSetInfos
}

func (c *replicaSetClient) ReplicaSetInfos() []*ReplicaSetInfo {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.replicaSetInfos
}

func newReplicaSetClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...replicaSetClientOption) (*replicaSetClient, error) {
	c := &replicaSetClient{
		stopChan:                  make(chan struct{}),
		cachedReplicaSetMap:       make(map[string]time.Time),
		replicaSetToDeploymentMap: make(map[string]string),
	}

	for _, option := range options {
		option(c)
	}

	c.store = NewObjStore(transformFuncReplicaSet, logger)
	c.logger = logger

	c.informer = createSharedReplicasetsInformer(clientSet, c.store)
	go c.informer.Run(c.stopChan)

	if c.syncChecker != nil {
		if c.syncChecker.Check(c.informer, "Replicaset initial sync timeout") {
			if !cache.WaitForCacheSync(c.stopChan, c.informer.HasSynced) {
				c.logger.Warn("Replicaset informer cache sync timeout")
			}
		}
	}

	return c, nil
}

func (c *replicaSetClient) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()

	close(c.stopChan)
	c.stopped = true
}

func transformFuncReplicaSet(obj any) (any, error) {
	replicaSet, ok := obj.(*appsv1.ReplicaSet)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not ReplicaSet type", obj)
	}
	info := new(ReplicaSetInfo)
	info.Name = replicaSet.Name
	info.Namespace = replicaSet.Namespace
	info.Owners = []*ReplicaSetOwner{}
	for _, owner := range replicaSet.OwnerReferences {
		info.Owners = append(info.Owners, &ReplicaSetOwner{kind: owner.Kind, name: owner.Name})
	}
	info.Spec = &ReplicaSetSpec{
		Replicas: uint32(*replicaSet.Spec.Replicas),
	}
	info.Status = &ReplicaSetStatus{
		Replicas:          uint32(replicaSet.Status.Replicas),
		AvailableReplicas: uint32(replicaSet.Status.AvailableReplicas),
		ReadyReplicas:     uint32(replicaSet.Status.ReadyReplicas),
	}
	return info, nil
}

// createSharedReplicasetsInformer creates a shared informer for statefulsets
func createSharedReplicasetsInformer(clientSet kubernetes.Interface, store *ObjStore) cache.SharedIndexInformer {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Apps().V1().ReplicaSets().Informer()

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
