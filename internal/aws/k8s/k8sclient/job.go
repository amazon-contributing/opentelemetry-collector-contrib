// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	cronJob = "CronJob"
)

type JobClient interface {
	// get the mapping between job and cronjob
	JobToCronJob() map[string]string
}

type noOpJobClient struct {
}

func (nc *noOpJobClient) JobToCronJob() map[string]string {
	return map[string]string{}
}

func (nc *noOpJobClient) shutdown() {
}

type jobClientOption func(*jobClient)

func jobSyncCheckerOption(checker initialSyncChecker) jobClientOption {
	return func(j *jobClient) {
		j.syncChecker = checker
	}
}

type jobClient struct {
	stopChan chan struct{}
	stopped  bool

	store    *ObjStore
	informer cache.SharedIndexInformer

	syncChecker initialSyncChecker

	mu              sync.RWMutex
	cachedJobMap    map[string]time.Time
	jobToCronJobMap map[string]string
	logger          *zap.Logger
}

func (c *jobClient) JobToCronJob() map[string]string {
	if c.store.GetResetRefreshStatus() {
		c.refresh()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.jobToCronJobMap
}

func (c *jobClient) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()

	objsList := c.store.List()

	tmpMap := make(map[string]string)
	for _, obj := range objsList {
		job, ok := obj.(*jobInfo)
		if !ok {
			continue
		}
		for _, owner := range job.owners {
			if owner.kind == cronJob && owner.name != "" {
				tmpMap[job.name] = owner.name
				break
			}
		}
	}

	lastRefreshTime := time.Now()

	for k, v := range c.cachedJobMap {
		if lastRefreshTime.Sub(v) > cacheTTL {
			delete(c.jobToCronJobMap, k)
			delete(c.cachedJobMap, k)
		}
	}

	for k, v := range tmpMap {
		c.jobToCronJobMap[k] = v
		c.cachedJobMap[k] = lastRefreshTime
	}
}

func newJobClient(clientSet kubernetes.Interface, logger *zap.Logger, options ...jobClientOption) (*jobClient, error) {
	c := &jobClient{
		jobToCronJobMap: make(map[string]string),
		cachedJobMap:    make(map[string]time.Time),
		stopChan:        make(chan struct{}),
	}

	for _, option := range options {
		option(c)
	}

	c.store = NewObjStore(transformFuncJob, logger)
	c.logger = logger

	c.informer = createSharedJobsInformer(clientSet, c.store)
	go c.informer.Run(c.stopChan)

	if c.syncChecker != nil {
		if c.syncChecker.Check(c.informer, "Jobs initial sync timeout") {
			if !cache.WaitForCacheSync(c.stopChan, c.informer.HasSynced) {
				c.logger.Warn("Jobs informer cache sync timeout")
			}
		}
	}

	return c, nil
}

func (c *jobClient) shutdown() {
	close(c.stopChan)
	c.stopped = true
}

func transformFuncJob(obj any) (any, error) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return nil, fmt.Errorf("input obj %v is not Job type", obj)
	}
	info := new(jobInfo)
	info.name = job.Name
	info.owners = []*jobOwner{}
	for _, owner := range job.OwnerReferences {
		info.owners = append(info.owners, &jobOwner{kind: owner.Kind, name: owner.Name})
	}
	return info, nil
}

// createSharedJobsInformer creates a shared informer for jobs
func createSharedJobsInformer(clientSet kubernetes.Interface, store *ObjStore) cache.SharedIndexInformer {
	informerFactory := informers.NewSharedInformerFactory(clientSet, 0)
	sharedIndexInformer := informerFactory.Batch().V1().Jobs().Informer()

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
