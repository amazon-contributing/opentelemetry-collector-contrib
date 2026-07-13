// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	sourceArnHeaderKey     = "x-amz-source-arn"
	sourceAccountHeaderKey = "x-amz-source-account"
	envSourceAccount       = "AMZ_SOURCE_ACCOUNT"
	envSourceArn           = "AMZ_SOURCE_ARN"
)

// stsCredentialsProvider retrieves credentials from the regional STS endpoint, falling back to
// the partition's primary endpoint when the region is disabled.
type stsCredentialsProvider struct {
	// fallback latches onto partitional after the first RegionDisabledException. Atomic so the
	// latch is race-safe when callers use this provider unwrapped.
	fallback    atomic.Pointer[aws.CredentialsProvider]
	regional    aws.CredentialsProvider
	partitional aws.CredentialsProvider
}

var _ aws.CredentialsProvider = (*stsCredentialsProvider)(nil)

func (p *stsCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	if fb := p.fallback.Load(); fb != nil {
		return (*fb).Retrieve(ctx)
	}
	creds, err := p.regional.Retrieve(ctx)
	if err != nil {
		var rde *types.RegionDisabledException
		if errors.As(err, &rde) {
			p.fallback.Store(&p.partitional)
			return p.partitional.Retrieve(ctx)
		}
	}
	return creds, err
}

var getPartitionPrimaryRegion = awsv2.GetPartitionPrimaryRegion

// newRegionalFallbackCredentialsProvider builds a credentials provider from the given builder function,
// with fallback to the partition's primary STS endpoint on RegionDisabledException. The builder
// receives a regional aws.Config and returns a provider for that region.
func newRegionalFallbackCredentialsProvider(cfg aws.Config, region string, build func(aws.Config) aws.CredentialsProvider) aws.CredentialsProvider {
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	regional := build(regionalCfg)

	fallback := getPartitionPrimaryRegion(region)
	if fallback == "" {
		return regional
	}
	partitionalCfg := cfg.Copy()
	partitionalCfg.Region = fallback
	return &stsCredentialsProvider{
		regional:    regional,
		partitional: build(partitionalCfg),
	}
}

// newAssumeRoleClient is overrideable in tests.
var newAssumeRoleClient = func(cfg aws.Config) stscreds.AssumeRoleAPIClient {
	return newStsClient(cfg)
}

// newAssumeRoleCredentialsProvider returns a credentials provider that assumes roleARN via STS
// AssumeRole, with regional/partitional fallback on RegionDisabledException.
func newAssumeRoleCredentialsProvider(cfg aws.Config, roleARN, region, externalID string) aws.CredentialsProvider {
	return newRegionalFallbackCredentialsProvider(cfg, region, func(regionalCfg aws.Config) aws.CredentialsProvider {
		opts := func(o *stscreds.AssumeRoleOptions) {
			if externalID != "" {
				o.ExternalID = &externalID
			}
		}
		return stscreds.NewAssumeRoleProvider(newAssumeRoleClient(regionalCfg), roleARN, opts)
	})
}

// newWebIdentityClient is overrideable in tests.
var newWebIdentityClient = func(cfg aws.Config) stscreds.AssumeRoleWithWebIdentityAPIClient {
	return newStsClient(cfg)
}

// newWebIdentityCredentialsProvider returns a credentials provider that exchanges a web identity
// token for temporary credentials via STS AssumeRoleWithWebIdentity, with regional/partitional
// fallback on RegionDisabledException.
func newWebIdentityCredentialsProvider(cfg aws.Config, roleARN, region string, tokenRetriever stscreds.IdentityTokenRetriever) aws.CredentialsProvider {
	return newRegionalFallbackCredentialsProvider(cfg, region, func(regionalCfg aws.Config) aws.CredentialsProvider {
		return stscreds.NewWebIdentityRoleProvider(newWebIdentityClient(regionalCfg), roleARN, tokenRetriever)
	})
}

// newStsClient creates an STS client and, when both confused-deputy environment variables are set, appends
// headers that let resource-based policies limit the service's permissions to a specific resource.
// See https://docs.aws.amazon.com/IAM/latest/UserGuide/confused-deputy.html
func newStsClient(cfg aws.Config) *sts.Client {
	var optFns []func(*sts.Options)
	sourceAccount := os.Getenv(envSourceAccount)
	sourceArn := os.Getenv(envSourceArn)
	if sourceAccount != "" && sourceArn != "" {
		optFns = append(optFns, func(o *sts.Options) {
			o.APIOptions = append(o.APIOptions, func(s *smithymiddleware.Stack) error {
				return s.Build.Add(&confusedDeputyHeaders{sourceArn: sourceArn, sourceAccount: sourceAccount}, smithymiddleware.Before)
			})
		})
	}
	return sts.NewFromConfig(cfg, optFns...)
}

type confusedDeputyHeaders struct {
	sourceArn     string
	sourceAccount string
}

func (*confusedDeputyHeaders) ID() string { return "ConfusedDeputyHeaders" }

func (m *confusedDeputyHeaders) HandleBuild(ctx context.Context, in smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
	req, ok := in.Request.(*smithyhttp.Request)
	if !ok {
		return smithymiddleware.BuildOutput{}, smithymiddleware.Metadata{}, fmt.Errorf("unrecognized transport type %T", in.Request)
	}
	req.Header.Set(sourceArnHeaderKey, m.sourceArn)
	req.Header.Set(sourceAccountHeaderKey, m.sourceAccount)
	return next.HandleBuild(ctx, in)
}
