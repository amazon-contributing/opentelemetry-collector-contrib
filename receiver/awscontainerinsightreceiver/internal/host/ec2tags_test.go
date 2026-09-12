// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	ci "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/containerinsight"
)

type mockEC2TagsClient func(ctx context.Context, input *ec2.DescribeTagsInput, optFns ...func(options *ec2.Options)) (*ec2.DescribeTagsOutput, error)

func (m mockEC2TagsClient) DescribeTags(ctx context.Context, input *ec2.DescribeTagsInput, optFns ...func(options *ec2.Options)) (*ec2.DescribeTagsOutput, error) {
	return m(ctx, input, optFns...)
}

func TestEC2TagsForEKS(t *testing.T) {
	tests := []struct {
		name   string
		client func(t *testing.T) ec2TagsClient
	}{
		{
			name: "EKS",
			client: func(t *testing.T) ec2TagsClient {
				return mockEC2TagsClient(func(_ context.Context, _ *ec2.DescribeTagsInput, _ ...func(*ec2.Options)) (*ec2.DescribeTagsOutput, error) {
					t.Helper()
					return &ec2.DescribeTagsOutput{
						Tags: []ec2types.TagDescription{
							{
								Key:   aws.String(clusterNameTagKeyPrefix + "cluster-name"),
								Value: aws.String("owned"),
							},
							{
								Key:   aws.String(autoScalingGroupNameTag),
								Value: aws.String("asg"),
							},
						},
					}, nil
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			et := ec2Tags{
				containerOrchestrator: ci.EKS,
				client:                test.client(t),
				instanceID:            "instanceId",
				refreshInterval:       time.Millisecond,
				logger:                zap.NewNop(),
			}
			et.refresh(t.Context())
			assert.Equal(t, "cluster-name", et.getClusterName())
			assert.Equal(t, "asg", et.getAutoScalingGroupName())
		})
	}
}

func TestEC2TagsForECS(t *testing.T) {
	tests := []struct {
		name   string
		client func(t *testing.T) ec2TagsClient
	}{
		{
			name: "ECS",
			client: func(t *testing.T) ec2TagsClient {
				return mockEC2TagsClient(func(_ context.Context, _ *ec2.DescribeTagsInput, _ ...func(*ec2.Options)) (*ec2.DescribeTagsOutput, error) {
					t.Helper()
					return &ec2.DescribeTagsOutput{
						Tags: []ec2types.TagDescription{
							{
								Key:   aws.String(autoScalingGroupNameTag),
								Value: aws.String("asg"),
							},
						},
					}, nil
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			et := ec2Tags{
				containerOrchestrator: ci.ECS,
				client:                test.client(t),
				instanceID:            "instanceId",
				refreshInterval:       time.Millisecond,
				logger:                zap.NewNop(),
			}
			et.refresh(t.Context())
			assert.Equal(t, "asg", et.getAutoScalingGroupName())
		})
	}
}

// sentinelHTTPClient fails any request; the constructor tests use it to assert
// the EC2 clients do not inherit a custom HTTP client from the aws.Config.
type sentinelHTTPClient struct{}

func (*sentinelHTTPClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("sentinel HTTP client must not be used")
}

func TestNewEC2TagsUsesDefaultHTTPClient(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := aws.Config{
		HTTPClient:       &sentinelHTTPClient{},
		BaseEndpoint:     aws.String("https://sentinel.example.com"),
		RetryMaxAttempts: 42,
		APIOptions:       []func(*middleware.Stack) error{func(*middleware.Stack) error { return nil }},
	}
	provider := newEC2Tags(ctx, cfg, "instanceId", "us-east-1", ci.EKS, time.Minute, zap.NewNop(),
		func(et *ec2Tags) { et.maxJitterTime = 0 })

	opts := provider.(*ec2Tags).client.(*ec2.Client).Options()
	assert.IsType(t, &awshttp.BuildableClient{}, opts.HTTPClient,
		"EC2 client must use the SDK default HTTP client, not the config's custom client")
	assert.Nil(t, opts.BaseEndpoint,
		"EC2 client must use the SDK default endpoint resolution, not the config's custom endpoint")
	assert.Equal(t, 0, opts.RetryMaxAttempts,
		"EC2 client must use the SDK default retry attempts, not the config's retry budget")
	assert.Len(t, opts.APIOptions, 1, "APIOptions (middleware) must be preserved")
	assert.Equal(t, "us-east-1", opts.Region)
}
