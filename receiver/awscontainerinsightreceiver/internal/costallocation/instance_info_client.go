// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"go.uber.org/zap"
)

// InstanceInfoClient retrieves and caches hardware specs from EC2 DescribeInstanceTypes.
type InstanceInfoClient struct {
	client          ec2iface.EC2API
	cache           map[string]InstanceSpec // instanceType -> spec
	cacheMu         sync.RWMutex
	refreshInterval time.Duration
	logger          *zap.Logger
	stopCh          chan struct{}
}

// NewInstanceInfoClient creates a new InstanceInfoClient.
func NewInstanceInfoClient(client ec2iface.EC2API, refreshInterval time.Duration, logger *zap.Logger) *InstanceInfoClient {
	return &InstanceInfoClient{
		client:          client,
		cache:           make(map[string]InstanceSpec),
		refreshInterval: refreshInterval,
		logger:          logger,
		stopCh:          make(chan struct{}),
	}
}

// GetSpec returns the cached hardware spec for the given instance type.
func (ic *InstanceInfoClient) GetSpec(instanceType string) (InstanceSpec, bool) {
	ic.cacheMu.RLock()
	defer ic.cacheMu.RUnlock()
	spec, ok := ic.cache[instanceType]
	return spec, ok
}

// Refresh fetches instance type specifications from EC2 DescribeInstanceTypes.
func (ic *InstanceInfoClient) Refresh(ctx context.Context) error {
	newCache := make(map[string]InstanceSpec)
	input := &ec2.DescribeInstanceTypesInput{}

	err := ic.client.DescribeInstanceTypesPagesWithContext(ctx, input, func(page *ec2.DescribeInstanceTypesOutput, lastPage bool) bool {
		for _, info := range page.InstanceTypes {
			if info.InstanceType == nil {
				continue
			}
			spec := InstanceSpec{}
			if info.VCpuInfo != nil && info.VCpuInfo.DefaultVCpus != nil {
				spec.VCPUs = *info.VCpuInfo.DefaultVCpus
			}
			if info.MemoryInfo != nil && info.MemoryInfo.SizeInMiB != nil {
				spec.MemoryGiB = float64(*info.MemoryInfo.SizeInMiB) / 1024.0
			}
			if info.GpuInfo != nil && len(info.GpuInfo.Gpus) > 0 {
				var totalGPUs int64
				for _, gpu := range info.GpuInfo.Gpus {
					if gpu.Count != nil {
						totalGPUs += *gpu.Count
					}
				}
				spec.GPUCount = totalGPUs
			}
			newCache[aws.StringValue(info.InstanceType)] = spec
		}
		return !lastPage
	})
	if err != nil {
		return fmt.Errorf("DescribeInstanceTypes API call failed: %w", err)
	}

	if len(newCache) == 0 {
		return errors.New("DescribeInstanceTypes returned no results")
	}

	ic.cacheMu.Lock()
	ic.cache = newCache
	ic.cacheMu.Unlock()

	ic.logger.Info("Refreshed instance spec cache", zap.Int("instanceTypes", len(newCache)))
	return nil
}

// Start begins the periodic cache refresh goroutine.
func (ic *InstanceInfoClient) Start(ctx context.Context) {
	if err := ic.Refresh(ctx); err != nil {
		ic.logger.Warn("Initial instance info refresh failed", zap.Error(err))
	}

	go func() {
		ticker := time.NewTicker(ic.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := ic.Refresh(ctx); err != nil {
					ic.logger.Warn("Instance info cache refresh failed, using stale data", zap.Error(err))
				}
			case <-ic.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Shutdown stops the periodic refresh goroutine.
func (ic *InstanceInfoClient) Shutdown() {
	close(ic.stopCh)
}
