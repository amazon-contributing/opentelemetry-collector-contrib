// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package aws // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"go.uber.org/zap"
)

// IMDSClient wraps a pair of EC2 instance-metadata clients and applies a
// strict-then-permissive fallback strategy to every call.
//
// The strict client uses IMDSRetryer (so transient IMDS errors are retried)
// and disables IMDSv1 fallback (EnableFallback=FalseTernary), i.e. it is
// IMDSv2-only. If a call against the strict client fails, the same call is
// retried against the permissive client, which enables IMDSv1 fallback
// (EnableFallback=TrueTernary).
//
// IMDSClient mirrors the method set of *imds.Client, so it can be used as a
// drop-in replacement and satisfies the narrow IMDS interfaces some callers
// declare for testability.
type IMDSClient struct {
	strict     *imds.Client
	permissive *imds.Client
	logger     *zap.Logger
}

// NewIMDSClient builds an IMDSClient from explicit imds.Options functional
// options. Use this when no aws.Config is available yet (for example, during
// region resolution that runs before the config is loaded). Caller-supplied
// optFns are applied first; the strict/permissive fallback settings are
// applied last so they always take effect.
//
// retries is the number of additional attempts the strict client makes on
// retryable IMDS errors. logger may be nil; when set, a debug line is emitted
// whenever the strict client fails and the permissive client is tried.
func NewIMDSClient(logger *zap.Logger, retries int, optFns ...func(*imds.Options)) *IMDSClient {
	return &IMDSClient{
		strict:     imds.New(imds.Options{}, strictOptions(retries, optFns)...),
		permissive: imds.New(imds.Options{}, permissiveOptions(optFns)...),
		logger:     logger,
	}
}

// NewIMDSClientFromConfig builds an IMDSClient from an aws.Config. The config's
// HTTPClient, Region, and other relevant settings flow through to both
// underlying clients. See NewIMDSClient for the meaning of logger and retries.
func NewIMDSClientFromConfig(cfg aws.Config, logger *zap.Logger, retries int, optFns ...func(*imds.Options)) *IMDSClient {
	return &IMDSClient{
		strict:     imds.NewFromConfig(cfg, strictOptions(retries, optFns)...),
		permissive: imds.NewFromConfig(cfg, permissiveOptions(optFns)...),
		logger:     logger,
	}
}

// strictOptions returns the caller options followed by the strict-client
// settings (IMDSRetryer + IMDSv2-only). The trailing entry wins, so callers
// cannot accidentally re-enable fallback on the strict client.
func strictOptions(retries int, optFns []func(*imds.Options)) []func(*imds.Options) {
	strict := func(o *imds.Options) {
		o.Retryer = NewIMDSRetryer(retries)
		o.EnableFallback = aws.FalseTernary
	}
	return append(append([]func(*imds.Options){}, optFns...), strict)
}

// permissiveOptions returns the caller options followed by the permissive-client
// setting (IMDSv1 fallback enabled).
func permissiveOptions(optFns []func(*imds.Options)) []func(*imds.Options) {
	permissive := func(o *imds.Options) {
		o.EnableFallback = aws.TrueTernary
	}
	return append(append([]func(*imds.Options){}, optFns...), permissive)
}

// GetMetadata retrieves the value at the given IMDS path, trying the strict
// client first and falling back to the permissive client on error.
func (c *IMDSClient) GetMetadata(ctx context.Context, params *imds.GetMetadataInput, optFns ...func(*imds.Options)) (*imds.GetMetadataOutput, error) {
	return withFallback(c, func(client *imds.Client) (*imds.GetMetadataOutput, error) {
		return client.GetMetadata(ctx, params, optFns...)
	})
}

// GetRegion retrieves the region, trying the strict client first and falling
// back to the permissive client on error.
func (c *IMDSClient) GetRegion(ctx context.Context, params *imds.GetRegionInput, optFns ...func(*imds.Options)) (*imds.GetRegionOutput, error) {
	return withFallback(c, func(client *imds.Client) (*imds.GetRegionOutput, error) {
		return client.GetRegion(ctx, params, optFns...)
	})
}

// GetInstanceIdentityDocument retrieves the instance identity document, trying
// the strict client first and falling back to the permissive client on error.
func (c *IMDSClient) GetInstanceIdentityDocument(ctx context.Context, params *imds.GetInstanceIdentityDocumentInput, optFns ...func(*imds.Options)) (*imds.GetInstanceIdentityDocumentOutput, error) {
	return withFallback(c, func(client *imds.Client) (*imds.GetInstanceIdentityDocumentOutput, error) {
		return client.GetInstanceIdentityDocument(ctx, params, optFns...)
	})
}

// withFallback runs fn against the strict client; if that errors, it logs (when
// a logger is configured) and retries against the permissive client. It is a
// free function rather than a method because Go does not allow type parameters
// on methods.
func withFallback[T any](c *IMDSClient, fn func(*imds.Client) (T, error)) (T, error) {
	result, err := fn(c.strict)
	if err == nil {
		return result, nil
	}
	if c.logger != nil {
		c.logger.Debug("strict IMDSv2 client failed; retrying with permissive fallback client", zap.Error(err))
	}
	return fn(c.permissive)
}
