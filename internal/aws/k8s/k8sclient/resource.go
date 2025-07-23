// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sclient // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/k8s/k8sclient"

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Client is a generic interface for Kubernetes resource clients.
type Client interface {
	TotalCount() int // Returns the total number of resources currently observed.
	shutdown()
}

type CountByNamespaceClient interface {
	Client
	CountByNamespace() map[string]int // CountByNamespace returns a map of namespace to the count of resources in that namespace.
}

type noOpClient struct{}

func (n *noOpClient) TotalCount() int { return 0 }
func (n *noOpClient) shutdown()       {}

// ResourceConfig holds the configuration needed to create a resource client
type ResourceConfig[T any] struct {
	ResourceName  string
	ListWatchFunc func(kubernetes.Interface, string) cache.ListerWatcher
	TransformFunc func(interface{}) (T, error)
	RefreshFunc   func([]T) (int, map[string]int)
	TestListFunc  func(kubernetes.Interface) error
	NewObjectFunc func() T
	Namespace     string
}

// BaseResourceClient provides common functionality for all resource clients
type BaseResourceClient[T any] struct {
	*ResourceClient[T]
	config ResourceConfig[T]
}

// NewBaseResourceClient creates a new base resource client with the given configuration
func NewBaseResourceClient[T any](
	clientSet kubernetes.Interface,
	logger *zap.Logger,
	config ResourceConfig[T],
	syncChecker initialSyncChecker,
) (*BaseResourceClient[T], error) {
	// Test connectivity
	if err := config.TestListFunc(clientSet); err != nil {
		return nil, fmt.Errorf("failed to list %s: %w", config.ResourceName, err)
	}

	// Create wrapper transform function for ObjStore
	transformFuncForStore := func(obj interface{}) (interface{}, error) {
		return config.TransformFunc(obj)
	}

	resourceClient := &ResourceClient[T]{
		stopChan:    make(chan struct{}),
		store:       NewObjStore(transformFuncForStore, logger),
		transformFn: config.TransformFunc,
		refreshFunc: config.RefreshFunc,
		syncChecker: syncChecker,
	}

	// Create and run Reflector
	lw := config.ListWatchFunc(clientSet, config.Namespace)
	reflector := cache.NewReflector(lw, config.NewObjectFunc(), resourceClient.store, 0)
	go reflector.Run(resourceClient.stopChan)

	// Check initial sync
	if resourceClient.syncChecker != nil {
		resourceClient.syncChecker.Check(reflector, fmt.Sprintf("%s initial sync timeout", config.ResourceName))
	}

	return &BaseResourceClient[T]{
		ResourceClient: resourceClient,
		config:         config,
	}, nil
}

// TotalCount implements the Client interface.
func (b *BaseResourceClient[T]) TotalCount() int {
	return b.GetTotalCount()
}

// Shutdown implements the Client interface.
func (b *BaseResourceClient[T]) shutdown() {
	b.ResourceClient.shutdown()
}

type ResourceClient[T any] struct {
	stopChan    chan struct{}
	stopped     bool
	store       *ObjStore
	syncChecker initialSyncChecker

	refreshFunc func([]T) (total int, extra map[string]int)
	transformFn func(interface{}) (T, error)

	mu         sync.RWMutex
	totalCount int
	extraMap   map[string]int
}

func (r *ResourceClient[T]) refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()

	var objs []T
	objsList := r.store.List()
	for _, obj := range objsList {
		transformedObj, err := r.transformFn(obj)
		if err != nil {
			continue // Skip invalid objects
		}
		objs = append(objs, transformedObj)
	}

	totalCount, extraMap := r.refreshFunc(objs)
	r.totalCount = totalCount
	r.extraMap = extraMap
}

func (rc *ResourceClient[T]) GetTotalCount() int {
	if rc.store.GetResetRefreshStatus() {
		rc.refresh()
	}
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.totalCount
}

func (rc *ResourceClient[T]) GetExtraMap() map[string]int {
	if rc.store.GetResetRefreshStatus() {
		rc.refresh()
	}
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	// Return a copy
	out := make(map[string]int, len(rc.extraMap))
	for k, v := range rc.extraMap {
		out[k] = v
	}
	return out
}

func (rc *ResourceClient[T]) shutdown() {
	if !rc.stopped {
		close(rc.stopChan)
		rc.stopped = true
	}
}
