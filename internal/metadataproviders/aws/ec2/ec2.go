// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ec2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/aws/ec2"

import (
	"context"

	"github.com/aws/aws-sdk-go/aws/client"
)

// Metadata is a set of information about the EC2 instance.
type Metadata struct {
	AccountID        string
	AvailabilityZone string
	Hostname         string
	ImageID          string
	InstanceID       string
	InstanceType     string
	PrivateIP        string
	Region           string
}

// Provider provides functions to get EC2 Metadata and the hostname.
type Provider interface {
	Get(ctx context.Context) (*Metadata, error)
	Hostname(ctx context.Context) (string, error)
	ID() string
}

func NewProvider(configProvider client.ConfigProvider, options ...Option) Provider {
	cfg := DefaultConfig().WithOptions(options...)
	return newChainMetadataProvider(
		[]Provider{
			newIMDSv2MetadataProvider(configProvider, cfg.IMDSv2Retries),
			newIMDSv1MetadataProvider(configProvider),
			newDescribeInstancesMetadataProvider(configProvider),
		},
	)
}
