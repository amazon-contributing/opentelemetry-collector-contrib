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

type DeploymentClient interface {
	// DeploymentInfos contains the information about each deployment in the cluster
	DeploymentInfos() []*DeploymentInfo
}

type noOpDeploymentClient struct {
}

func (nd *noOpDeploymentClient) DeploymentInfos() []*DeploymentInfo {
	return []*DeploymentInfo{}
}

func (nd *noOpDeploymentClient) shutdown() {
}

type deploymentClientOption func(*deploymentClient)

func deploymentSyncCheckerOption(checker initialSyncChecker) deploymentClientOption {
	return func(d *deploymentClient) {
		d.syncChecker = checker
	}
}

type deploymentClient struct {
	stopChan chan struct{}
	stopped  bool

	store    *ObjStore
	informer cache.SharedIndexInformer

	syncChecker initialSyncChecker

	mu              sync.RWMutex
	deploymentInfos []*DeploymentInfo
	logger          *zap.Logger
}

func (d *deploymentClient) refresh() {
	d.mu.Lock()
	defer d.mu.Unlock()

	var deploymentInfos []*DeploymentInfo
	objsList := d.store.List()
	for _, obj := range objsList {
		deployment, ok := obj.(*DeploymentInfo)
		if !ok {
			continue
		}
		deploymentInfos = append(deploymentInfos, deployment)
	}

	d.deploymentInfos = deploymentInfos
}

func (d *deploymentClient) DeploymentInfos() []*DeploymentInfo {
	if d.store.GetResetRefreshStatus() {
		d.refresh()
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.deploymentInfos
}

func newDeploymentClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...deploymentClientOption) (*deploymentClient, error) {
	d := &deploymentClient{
		stopChan: make(chan struct{}),
	}

	for _, option := range options {
		option(d)
	}

	d.store = NewObjStore(transformFuncDeployment, logger)
	d.logger = logger

	d.informer = createSharedDeploymentsInformer(clientSet, d.store)
	go d.informer.Run(d.stopChan)

	if d.syncChecker != nil {
		if d.syncChecker.Check(d.informer, "Deployment initial sync timeout") {
			if !cache.WaitForCacheSync(d.stopChan, d.informer.HasSynced) {
				d.logger.Warn("Deployment informer cache sync timeout")
			}
		}
	}

	return d, nil
}

func (d *deploymentClient) shutdown() {
	d.mu.Lock()
	defer d.mu.Unlock()
	close(d.stopChan)
	d.stopped = true
}

func transformFuncDeployment(obj any) (any, error) {
	deployment, ok := obj.(*appsv1.Deployment)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not Deployment type", obj)
	}
	info := new(DeploymentInfo)
	info.Name = deployment.Name
	info.Namespace = deployment.Namespace
	info.Spec = &DeploymentSpec{
		Replicas: uint32(*deployment.Spec.Replicas),
	}
	info.Status = &DeploymentStatus{
		Replicas:            uint32(deployment.Status.Replicas),
		ReadyReplicas:       uint32(deployment.Status.ReadyReplicas),
		AvailableReplicas:   uint32(deployment.Status.AvailableReplicas),
		UnavailableReplicas: uint32(deployment.Status.UnavailableReplicas),
	}
	return info, nil
}

// createSharedDeploymentsInformer creates a shared informer for deployments
func createSharedDeploymentsInformer(clientSet kubernetes.Interface, store *ObjStore) cache.SharedIndexInformer {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Apps().V1().Deployments().Informer()

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
