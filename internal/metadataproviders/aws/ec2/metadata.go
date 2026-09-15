// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ec2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/aws/ec2"

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
)

type Provider interface {
	Get(ctx context.Context) (imds.InstanceIdentityDocument, error)
	Hostname(ctx context.Context) (string, error)
	InstanceID(ctx context.Context) (string, error)
	Tags(ctx context.Context) ([]string, error)
	Tag(ctx context.Context, key string) (string, error)
}

// IMDSClient is the subset of the EC2 IMDS client API used by the provider. It
// is satisfied by *imds.Client and by wrappers with the same method set.
type IMDSClient interface {
	GetMetadata(ctx context.Context, params *imds.GetMetadataInput, optFns ...func(*imds.Options)) (*imds.GetMetadataOutput, error)
	GetInstanceIdentityDocument(ctx context.Context, params *imds.GetInstanceIdentityDocumentInput, optFns ...func(*imds.Options)) (*imds.GetInstanceIdentityDocumentOutput, error)
}

type metadataClient struct {
	client IMDSClient
}

var _ Provider = (*metadataClient)(nil)

// NewProvider returns a Provider backed by the default IMDS client built from cfg.
func NewProvider(cfg aws.Config) Provider {
	return NewProviderFromClient(imds.NewFromConfig(cfg))
}

// NewProviderFromClient returns a Provider backed by the supplied IMDS client.
func NewProviderFromClient(client IMDSClient) Provider {
	return &metadataClient{client: client}
}

func (c *metadataClient) getMetadata(ctx context.Context, path string) (string, error) {
	output, err := c.client.GetMetadata(ctx, &imds.GetMetadataInput{Path: path})
	if err != nil {
		return "", fmt.Errorf("failed to get %s from IMDS: %w", path, err)
	}
	defer output.Content.Close()

	data, err := io.ReadAll(output.Content)
	if err != nil {
		return "", fmt.Errorf("failed to read %s response: %w", path, err)
	}

	return string(data), nil
}

func (c *metadataClient) InstanceID(ctx context.Context) (string, error) {
	return c.getMetadata(ctx, "instance-id")
}

func (c *metadataClient) Hostname(ctx context.Context) (string, error) {
	return c.getMetadata(ctx, "hostname")
}

func (c *metadataClient) Get(ctx context.Context) (imds.InstanceIdentityDocument, error) {
	output, err := c.client.GetInstanceIdentityDocument(ctx, &imds.GetInstanceIdentityDocumentInput{})
	if err != nil {
		return imds.InstanceIdentityDocument{}, fmt.Errorf("failed to get instance identity document: %w", err)
	}

	return output.InstanceIdentityDocument, nil
}

func (c *metadataClient) Tags(ctx context.Context) ([]string, error) {
	tagKeysRaw, err := c.getMetadata(ctx, "tags/instance")
	if err != nil {
		return nil, fmt.Errorf("failed to list tag keys from IMDS: %w", err)
	}

	var keys []string
	for key := range strings.SplitSeq(tagKeysRaw, "\n") {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (c *metadataClient) Tag(ctx context.Context, key string) (string, error) {
	return c.getMetadata(ctx, "tags/instance/"+key)
}
