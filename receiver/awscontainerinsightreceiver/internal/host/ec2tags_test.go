// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Setenv("AWS_CA_BUNDLE", writeSelfSignedCertForTest(t))
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
	tr := opts.HTTPClient.(*awshttp.BuildableClient).GetTransport()
	assert.True(t, tr.TLSClientConfig != nil && tr.TLSClientConfig.RootCAs != nil,
		"EC2 client must still honor AWS_CA_BUNDLE")
	assert.Nil(t, opts.BaseEndpoint,
		"EC2 client must use the SDK default endpoint resolution, not the config's custom endpoint")
	assert.Equal(t, 0, opts.RetryMaxAttempts,
		"EC2 client must use the SDK default retry attempts, not the config's retry budget")
	assert.Len(t, opts.APIOptions, 1, "APIOptions (middleware) must be preserved")
	assert.Equal(t, "us-east-1", opts.Region)
}

// writeSelfSignedCertForTest writes a self-signed cert PEM to a temp file and
// returns its path.
func writeSelfSignedCertForTest(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	f := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(f, pemBytes, 0o600))
	return f
}
