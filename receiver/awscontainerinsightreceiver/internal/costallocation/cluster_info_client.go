// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver/internal/costallocation"

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"go.uber.org/zap"
)

// EKSDescribeClusterAPI is the subset of the EKS v2 client needed by ClusterInfoClient.
// Using a minimal interface makes it easy to mock in tests.
type EKSDescribeClusterAPI interface {
	DescribeCluster(ctx context.Context, params *eks.DescribeClusterInput, optFns ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
}

// ClusterInfoClient retrieves EKS cluster fee parameters from DescribeCluster.
type ClusterInfoClient struct {
	client          EKSDescribeClusterAPI
	clusterName     string
	fees            ClusterFees
	feesMu          sync.RWMutex
	refreshInterval time.Duration
	logger          *zap.Logger
	stopCh          chan struct{}
}

// NewClusterInfoClient creates a new ClusterInfoClient.
func NewClusterInfoClient(client EKSDescribeClusterAPI, clusterName string, refreshInterval time.Duration, logger *zap.Logger) *ClusterInfoClient {
	return &ClusterInfoClient{
		client:          client,
		clusterName:     clusterName,
		refreshInterval: refreshInterval,
		logger:          logger,
		stopCh:          make(chan struct{}),
	}
}

// GetFees returns the cached cluster fee data.
func (cc *ClusterInfoClient) GetFees() ClusterFees {
	cc.feesMu.RLock()
	defer cc.feesMu.RUnlock()
	return cc.fees
}

// Refresh fetches cluster fee parameters from EKS DescribeCluster.
func (cc *ClusterInfoClient) Refresh(ctx context.Context) error {
	output, err := cc.client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: &cc.clusterName,
	})
	if err != nil {
		return fmt.Errorf("DescribeCluster API call failed: %w", err)
	}
	if output.Cluster == nil {
		return fmt.Errorf("DescribeCluster returned nil cluster for %s", cc.clusterName)
	}

	fees := ClusterFees{}

	// Determine cluster fee from upgrade policy support type.
	fees.ClusterFee = mapSupportTypeToFee(output.Cluster)

	// Determine provisioned control plane fee from scaling config tier.
	fees.ProvisionedControlPlane = mapScalingTierToFee(output.Cluster)

	cc.feesMu.Lock()
	cc.fees = fees
	cc.feesMu.Unlock()

	cc.logger.Info("Refreshed cluster fee data",
		zap.Float64("clusterFee", fees.ClusterFee),
		zap.Float64("provisionedCPFee", fees.ProvisionedControlPlane))
	return nil
}

// mapSupportTypeToFee maps the EKS upgrade policy support type to the hourly cluster fee.
func mapSupportTypeToFee(cluster *types.Cluster) float64 {
	if cluster.UpgradePolicy != nil && cluster.UpgradePolicy.SupportType != "" {
		switch strings.ToUpper(string(cluster.UpgradePolicy.SupportType)) {
		case "EXTENDED":
			return 0.60
		case "STANDARD":
			return 0.10
		}
	}
	// Default to standard if not specified.
	return 0.10
}

// mapScalingTierToFee maps the EKS control plane scaling config tier to the hourly fee.
// Tier values: "standard" ($0), "tier-xl" ($1.65), "tier-2xl" ($3.40), "tier-4xl" ($6.90), "tier-8xl" ($13.90).
func mapScalingTierToFee(cluster *types.Cluster) float64 {
	if cluster.ControlPlaneScalingConfig == nil {
		return 0
	}
	switch cluster.ControlPlaneScalingConfig.Tier {
	case "tier-xl":
		return 1.65
	case "tier-2xl":
		return 3.40
	case "tier-4xl":
		return 6.90
	case "tier-8xl":
		return 13.90
	default: // "standard" or empty
		return 0
	}
}

// Start begins the periodic cache refresh goroutine.
func (cc *ClusterInfoClient) Start(ctx context.Context) {
	if err := cc.Refresh(ctx); err != nil {
		cc.logger.Warn("Initial cluster info refresh failed", zap.Error(err))
	}

	go func() {
		ticker := time.NewTicker(cc.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := cc.Refresh(ctx); err != nil {
					cc.logger.Warn("Cluster info cache refresh failed, using stale data", zap.Error(err))
				}
			case <-cc.stopCh:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Shutdown stops the periodic refresh goroutine.
func (cc *ClusterInfoClient) Shutdown() {
	close(cc.stopCh)
}
