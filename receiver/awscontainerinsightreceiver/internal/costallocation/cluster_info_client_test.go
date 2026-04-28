// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockEKSAPI struct {
	output *eks.DescribeClusterOutput
	err    error
}

func (m *mockEKSAPI) DescribeCluster(_ context.Context, _ *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.output, nil
}

func TestClusterInfoClient_StandardSupport(t *testing.T) {
	mock := &mockEKSAPI{
		output: &eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				UpgradePolicy: &types.UpgradePolicyResponse{
					SupportType: types.SupportTypeStandard,
				},
			},
		},
	}
	cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
	require.NoError(t, cc.Refresh(t.Context()))
	fees := cc.GetFees()
	assert.InDelta(t, 0.10, fees.ClusterFee, 1e-9)
	assert.InDelta(t, 0.0, fees.ProvisionedControlPlane, 1e-9)
}

func TestClusterInfoClient_ExtendedSupport(t *testing.T) {
	mock := &mockEKSAPI{
		output: &eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				UpgradePolicy: &types.UpgradePolicyResponse{
					SupportType: types.SupportTypeExtended,
				},
			},
		},
	}
	cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
	require.NoError(t, cc.Refresh(t.Context()))
	fees := cc.GetFees()
	assert.InDelta(t, 0.60, fees.ClusterFee, 1e-9)
}

func TestClusterInfoClient_ProvisionedTiers(t *testing.T) {
	tests := []struct {
		tier     types.ProvisionedControlPlaneTier
		expected float64
	}{
		{"standard", 0},
		{"tier-xl", 1.65},
		{"tier-2xl", 3.40},
		{"tier-4xl", 6.90},
		{"tier-8xl", 13.90},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			mock := &mockEKSAPI{
				output: &eks.DescribeClusterOutput{
					Cluster: &types.Cluster{
						UpgradePolicy: &types.UpgradePolicyResponse{
							SupportType: types.SupportTypeStandard,
						},
						ControlPlaneScalingConfig: &types.ControlPlaneScalingConfig{
							Tier: tt.tier,
						},
					},
				},
			}
			cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
			require.NoError(t, cc.Refresh(t.Context()))
			assert.InDelta(t, tt.expected, cc.GetFees().ProvisionedControlPlane, 1e-9)
		})
	}
}

func TestClusterInfoClient_NilScalingConfig(t *testing.T) {
	mock := &mockEKSAPI{
		output: &eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				UpgradePolicy: &types.UpgradePolicyResponse{
					SupportType: types.SupportTypeStandard,
				},
			},
		},
	}
	cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
	require.NoError(t, cc.Refresh(t.Context()))
	assert.InDelta(t, 0.0, cc.GetFees().ProvisionedControlPlane, 1e-9)
}

func TestClusterInfoClient_APIError_ColdCache(t *testing.T) {
	mock := &mockEKSAPI{err: errors.New("api error")}
	cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
	err := cc.Refresh(t.Context())
	assert.Error(t, err)
	fees := cc.GetFees()
	assert.InDelta(t, 0.0, fees.ClusterFee, 1e-9)
}

func TestClusterInfoClient_APIError_WarmCache(t *testing.T) {
	mock := &mockEKSAPI{
		output: &eks.DescribeClusterOutput{
			Cluster: &types.Cluster{
				UpgradePolicy: &types.UpgradePolicyResponse{
					SupportType: types.SupportTypeExtended,
				},
			},
		},
	}
	cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
	require.NoError(t, cc.Refresh(t.Context()))

	mock.err = errors.New("api error")
	assert.Error(t, cc.Refresh(t.Context()))

	fees := cc.GetFees()
	assert.InDelta(t, 0.60, fees.ClusterFee, 1e-9, "warm cache should survive API error")
}

func TestClusterInfoClient_DefaultSupportType(t *testing.T) {
	mock := &mockEKSAPI{
		output: &eks.DescribeClusterOutput{
			Cluster: &types.Cluster{},
		},
	}
	cc := NewClusterInfoClient(mock, "test-cluster", time.Hour, zap.NewNop())
	require.NoError(t, cc.Refresh(t.Context()))
	assert.InDelta(t, 0.10, cc.GetFees().ClusterFee, 1e-9, "should default to standard $0.10")
}
