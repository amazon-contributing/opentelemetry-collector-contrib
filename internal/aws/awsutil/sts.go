// Copyright The OpenTelemetry Authors
// Portions of this file Copyright 2018-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	override "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"
	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// stsClientTimeout bounds each STS request so an unresponsive backend
// cannot hang credential refresh.
const stsClientTimeout = time.Minute

// Confused Deputy Prevention header keys and the env vars that drive
// them. See https://docs.aws.amazon.com/IAM/latest/UserGuide/confused-deputy.html.
const (
	SourceArnHeaderKey     = "x-amz-source-arn"
	SourceAccountHeaderKey = "x-amz-source-account"
	AmzSourceAccount       = "AMZ_SOURCE_ACCOUNT"
	AmzSourceArn           = "AMZ_SOURCE_ARN"
)

// stsCredentialsProvider falls back from a regional STS endpoint to the
// partition's primary endpoint on RegionDisabledException, and latches
// onto the partitional client for all subsequent retrievals.
type stsCredentialsProvider struct {
	fallback              atomic.Pointer[aws.CredentialsProvider]
	regional, partitional aws.CredentialsProvider
}

var _ aws.CredentialsProvider = (*stsCredentialsProvider)(nil)

func (p *stsCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	if fb := p.fallback.Load(); fb != nil {
		return (*fb).Retrieve(ctx)
	}
	creds, err := p.regional.Retrieve(ctx)
	if err != nil {
		var rde *ststypes.RegionDisabledException
		if errors.As(err, &rde) && p.partitional != nil {
			p.fallback.Store(&p.partitional)
			return p.partitional.Retrieve(ctx)
		}
	}
	return creds, err
}

// newRegionalFallbackCredentialsProvider wraps build(regionalCfg) with automatic
// fallback to the partition's primary STS endpoint on RegionDisabledException.
// build is invoked once per endpoint with a region-scoped copy of cfg.
//
// If region cannot be resolved to a known partition, the partitional provider is
// not constructed; the provider behaves as regional-only and surfaces the
// regional error rather than retrying in the wrong partition.
func newRegionalFallbackCredentialsProvider(cfg aws.Config, region string, build func(aws.Config) aws.CredentialsProvider) aws.CredentialsProvider {
	regionalCfg := cfg.Copy()
	regionalCfg.Region = region

	p := &stsCredentialsProvider{regional: build(regionalCfg)}

	if fallback := override.GetPartitionPrimaryRegion(region); fallback != "" {
		partitionalCfg := cfg.Copy()
		partitionalCfg.Region = fallback
		p.partitional = build(partitionalCfg)
	}

	return p
}

// newAssumeRoleCredentialsProvider returns a provider that assumes roleARN against
// region's STS endpoint, with partitional fallback. externalID is threaded into
// stscreds.AssumeRoleOptions when non-empty.
func newAssumeRoleCredentialsProvider(cfg aws.Config, roleARN, region, externalID string) aws.CredentialsProvider {
	opts := func(o *stscreds.AssumeRoleOptions) {
		if externalID != "" {
			o.ExternalID = &externalID
		}
	}
	return newRegionalFallbackCredentialsProvider(cfg, region, func(c aws.Config) aws.CredentialsProvider {
		return stscreds.NewAssumeRoleProvider(newAssumeRoleClient(c), roleARN, opts)
	})
}

// newWebIdentityCredentialsProvider returns a provider that assumes roleARN via STS
// AssumeRoleWithWebIdentity using the OIDC token from tokenRetriever, with
// partitional fallback. The token is read lazily on Retrieve, so a token file that
// is not yet present at startup (e.g. a projected Kubernetes service-account token)
// does not fail configuration. externalID does not apply to web identity.
func newWebIdentityCredentialsProvider(cfg aws.Config, roleARN, region string, tokenRetriever stscreds.IdentityTokenRetriever) aws.CredentialsProvider {
	return newRegionalFallbackCredentialsProvider(cfg, region, func(c aws.Config) aws.CredentialsProvider {
		return stscreds.NewWebIdentityRoleProvider(newWebIdentityClient(c), roleARN, tokenRetriever)
	})
}

// newAssumeRoleClient and newWebIdentityClient are overrideable in tests.
// *sts.Client satisfies both interfaces, so both share the Confused Deputy
// middleware installed by newStsClient.
var (
	newAssumeRoleClient  = func(cfg aws.Config) stscreds.AssumeRoleAPIClient { return newStsClient(cfg) }
	newWebIdentityClient = func(cfg aws.Config) stscreds.AssumeRoleWithWebIdentityAPIClient { return newStsClient(cfg) }
)

// newStsClient builds an STS client. When both AmzSourceAccount and
// AmzSourceArn env vars are non-empty, registers a Build/Before
// middleware that stamps the Confused Deputy headers on every request.
// Build/Before runs ahead of SigV4 signing, so the headers are part of
// the signed request.
func newStsClient(cfg aws.Config) *sts.Client {
	var options []func(*sts.Options)

	// Preserves a client already resolved on the config (e.g. AWS_CA_BUNDLE).
	switch c := cfg.HTTPClient.(type) {
	case nil:
		cfg.HTTPClient = &http.Client{Timeout: stsClientTimeout}
	case *awshttp.BuildableClient:
		cfg.HTTPClient = c.WithTimeout(stsClientTimeout)
	}

	sourceAccount := os.Getenv(AmzSourceAccount)
	sourceArn := os.Getenv(AmzSourceArn)
	if sourceAccount != "" && sourceArn != "" {
		options = append(options, func(o *sts.Options) {
			o.APIOptions = append(o.APIOptions, func(s *smithymiddleware.Stack) error {
				return s.Build.Add(newCustomHeaderMiddleware("ConfusedDeputyHeaders", map[string]string{
					SourceArnHeaderKey:     sourceArn,
					SourceAccountHeaderKey: sourceAccount,
				}), smithymiddleware.Before)
			})
		})
		log.Printf("I! Found confused deputy header environment variables: source account: %q, source arn: %q",
			sourceAccount, sourceArn)
	}

	return sts.NewFromConfig(cfg, options...)
}

// customHeaderMiddleware sets a fixed set of HTTP headers on outgoing
// requests during the smithy Build step.
type customHeaderMiddleware struct {
	id      string
	headers map[string]string
}

var _ smithymiddleware.BuildMiddleware = (*customHeaderMiddleware)(nil)

func newCustomHeaderMiddleware(id string, headers map[string]string) *customHeaderMiddleware {
	return &customHeaderMiddleware{id: id, headers: headers}
}

func (m *customHeaderMiddleware) ID() string { return m.id }

func (m *customHeaderMiddleware) HandleBuild(
	ctx context.Context,
	in smithymiddleware.BuildInput,
	next smithymiddleware.BuildHandler,
) (smithymiddleware.BuildOutput, smithymiddleware.Metadata, error) {
	req, ok := in.Request.(*smithyhttp.Request)
	if !ok {
		return smithymiddleware.BuildOutput{}, smithymiddleware.Metadata{},
			fmt.Errorf("unrecognized transport type %T", in.Request)
	}
	for k, v := range m.headers {
		req.Header.Set(k, v)
	}
	return next.HandleBuild(ctx, in)
}
