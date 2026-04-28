// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package costallocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockEC2API struct {
	ec2iface.EC2API
	output *ec2.DescribeInstanceTypesOutput
	err    error
}

func (m *mockEC2API) DescribeInstanceTypesPagesWithContext(_ context.Context, _ *ec2.DescribeInstanceTypesInput, fn func(*ec2.DescribeInstanceTypesOutput, bool) bool, _ ...request.Option) error {
	if m.err != nil {
		return m.err
	}
	fn(m.output, true)
	return nil
}

func TestInstanceInfoClient_GetSpec_CacheHitMiss(t *testing.T) {
	mock := &mockEC2API{
		output: &ec2.DescribeInstanceTypesOutput{
			InstanceTypes: []*ec2.InstanceTypeInfo{
				{
					InstanceType: aws.String("m5.large"),
					VCpuInfo:     &ec2.VCpuInfo{DefaultVCpus: aws.Int64(2)},
					MemoryInfo:   &ec2.MemoryInfo{SizeInMiB: aws.Int64(8192)},
				},
				{
					InstanceType: aws.String("p3.2xlarge"),
					VCpuInfo:     &ec2.VCpuInfo{DefaultVCpus: aws.Int64(8)},
					MemoryInfo:   &ec2.MemoryInfo{SizeInMiB: aws.Int64(62464)},
					GpuInfo: &ec2.GpuInfo{
						Gpus: []*ec2.GpuDeviceInfo{
							{Count: aws.Int64(1)},
						},
					},
				},
			},
		},
	}
	ic := NewInstanceInfoClient(mock, time.Hour, zap.NewNop())
	err := ic.Refresh(t.Context())
	require.NoError(t, err)

	spec, ok := ic.GetSpec("m5.large")
	assert.True(t, ok)
	assert.Equal(t, int64(2), spec.VCPUs)
	assert.InDelta(t, 8.0, spec.MemoryGiB, 0.01)
	assert.Equal(t, int64(0), spec.GPUCount)

	spec, ok = ic.GetSpec("p3.2xlarge")
	assert.True(t, ok)
	assert.Equal(t, int64(8), spec.VCPUs)
	assert.Equal(t, int64(1), spec.GPUCount)

	_, ok = ic.GetSpec("nonexistent")
	assert.False(t, ok)
}

func TestInstanceInfoClient_Refresh_APIError_ColdCache(t *testing.T) {
	mock := &mockEC2API{err: errors.New("api error")}
	ic := NewInstanceInfoClient(mock, time.Hour, zap.NewNop())
	err := ic.Refresh(t.Context())
	assert.Error(t, err)

	_, ok := ic.GetSpec("m5.large")
	assert.False(t, ok)
}

func TestInstanceInfoClient_Refresh_APIError_WarmCache(t *testing.T) {
	mock := &mockEC2API{
		output: &ec2.DescribeInstanceTypesOutput{
			InstanceTypes: []*ec2.InstanceTypeInfo{
				{
					InstanceType: aws.String("m5.large"),
					VCpuInfo:     &ec2.VCpuInfo{DefaultVCpus: aws.Int64(2)},
					MemoryInfo:   &ec2.MemoryInfo{SizeInMiB: aws.Int64(8192)},
				},
			},
		},
	}
	ic := NewInstanceInfoClient(mock, time.Hour, zap.NewNop())
	require.NoError(t, ic.Refresh(t.Context()))

	mock.err = errors.New("api error")
	assert.Error(t, ic.Refresh(t.Context()))

	spec, ok := ic.GetSpec("m5.large")
	assert.True(t, ok, "warm cache should survive API error")
	assert.Equal(t, int64(2), spec.VCPUs)
}

func TestInstanceInfoClient_MemoryConversion(t *testing.T) {
	mock := &mockEC2API{
		output: &ec2.DescribeInstanceTypesOutput{
			InstanceTypes: []*ec2.InstanceTypeInfo{
				{
					InstanceType: aws.String("t3.micro"),
					VCpuInfo:     &ec2.VCpuInfo{DefaultVCpus: aws.Int64(2)},
					MemoryInfo:   &ec2.MemoryInfo{SizeInMiB: aws.Int64(1024)},
				},
			},
		},
	}
	ic := NewInstanceInfoClient(mock, time.Hour, zap.NewNop())
	require.NoError(t, ic.Refresh(t.Context()))

	spec, ok := ic.GetSpec("t3.micro")
	assert.True(t, ok)
	assert.InDelta(t, 1.0, spec.MemoryGiB, 1e-9, "1024 MiB should be 1.0 GiB")
}
