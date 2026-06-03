// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

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

// stsCredentialsProvider retrieves credentials from the regional STS endpoint, falling back to the
// partition's primary endpoint when the region is disabled.
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

func newStsCredentialsProvider(cfg aws.Config, roleARN, region, externalID string) aws.CredentialsProvider {
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region
	partitionalCfg := cfg.Copy()
	partitionalCfg.Region = getFallbackRegion(region)
	opts := func(o *stscreds.AssumeRoleOptions) {
		if externalID != "" {
			o.ExternalID = &externalID
		}
	}
	return &stsCredentialsProvider{
		regional:    stscreds.NewAssumeRoleProvider(newAssumeRoleClient(regionalCfg), roleARN, opts),
		partitional: stscreds.NewAssumeRoleProvider(newAssumeRoleClient(partitionalCfg), roleARN, opts),
	}
}

// newAssumeRoleClient is overrideable in tests.
var newAssumeRoleClient = newStsClient

// newStsClient creates an STS client and, when both confused-deputy environment variables are set, appends
// headers that let resource-based policies limit the service's permissions to a specific resource.
// See https://docs.aws.amazon.com/IAM/latest/UserGuide/confused-deputy.html
func newStsClient(cfg aws.Config) stscreds.AssumeRoleAPIClient {
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

// getFallbackRegion returns the partition's primary region, whose STS endpoint cannot be deactivated, used
// when the regional endpoint is disabled.
func getFallbackRegion(region string) string {
	switch {
	case strings.HasPrefix(region, "cn-"):
		return "cn-north-1"
	case strings.HasPrefix(region, "us-gov-"):
		return "us-gov-west-1"
	case strings.HasPrefix(region, "us-isob-"):
		return "us-isob-east-1"
	case strings.HasPrefix(region, "us-iso-"):
		return "us-iso-east-1"
	default:
		return "us-east-1"
	}
}
