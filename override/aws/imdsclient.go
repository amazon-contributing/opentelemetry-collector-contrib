// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package aws // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"

import (
	"context"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"go.uber.org/zap"
)

const ec2MetadataV1DisabledEnvVar = "AWS_EC2_METADATA_V1_DISABLED"

// IMDSClient wraps a pair of EC2 instance-metadata clients and applies a
// strict-then-permissive fallback strategy to every call.
//
// The strict client uses IMDSRetryer (so transient IMDS errors are retried)
// and disables IMDSv1 fallback (EnableFallback=FalseTernary), i.e. it is
// IMDSv2-only. If a call against the strict client fails, the same call is
// retried against the permissive client, which allows IMDSv1 fallback unless
// the operator opted out. Both constructors honor AWS_EC2_METADATA_V1_DISABLED
// from the environment; NewIMDSClientFromConfig additionally honors
// ec2_metadata_v1_disabled in shared config (via the SDK's own resolution).
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
// Because imds.New never consults the environment, the permissive client's
// IMDSv1 opt-out is resolved here from AWS_EC2_METADATA_V1_DISABLED at
// construction time. The shared config file (ec2_metadata_v1_disabled) is NOT
// consulted on this path; use NewIMDSClientFromConfig for that.
//
// retries is the number of additional attempts the strict client makes on
// retryable IMDS errors. logger may be nil; when set, a debug line is emitted
// whenever the strict client fails and the permissive client is tried.
func NewIMDSClient(logger *zap.Logger, retries int, optFns ...func(*imds.Options)) *IMDSClient {
	return &IMDSClient{
		strict:     imds.New(imds.Options{}, strictOptions(logger, retries, optFns)...),
		permissive: imds.New(imds.Options{}, permissiveOptions(logger, optFns)...),
		logger:     logger,
	}
}

// NewIMDSClientFromConfig builds an IMDSClient from an aws.Config. The config's
// Region, ConfigSources, APIOptions, and other relevant settings flow through
// to both underlying clients, but its HTTPClient is deliberately ignored: IMDS
// is only reachable at the link-local metadata endpoint, so a custom HTTP
// client carried by the config (for example, one with proxy or TLS settings
// intended for regional service calls) would break metadata lookups. Both
// underlying clients use the SDK default IMDS HTTP client with its fast-fail
// timeouts. See NewIMDSClient for the meaning of logger and retries.
//
// The permissive client's IMDSv1 opt-out is resolved in two layers: the SDK's
// own resolution reads cfg.ConfigSources, then AWS_EC2_METADATA_V1_DISABLED is
// read directly from the environment as a floor, honored even when cfg carries
// no ConfigSources. The floor only ever disables fallback.
func NewIMDSClientFromConfig(cfg aws.Config, logger *zap.Logger, retries int, optFns ...func(*imds.Options)) *IMDSClient {
	cfg.HTTPClient = nil
	return &IMDSClient{
		strict:     imds.NewFromConfig(cfg, strictOptions(logger, retries, optFns)...),
		permissive: imds.NewFromConfig(cfg, permissiveOptions(logger, optFns)...),
		logger:     logger,
	}
}

// strictOptions returns the caller options followed by the strict-client
// settings (IMDSRetryer + IMDSv2-only). The trailing entry wins, so callers
// cannot accidentally re-enable fallback on the strict client.
func strictOptions(logger *zap.Logger, retries int, optFns []func(*imds.Options)) []func(*imds.Options) {
	strict := func(o *imds.Options) {
		o.Retryer = NewIMDSRetryer(retries).WithLogger(logger)
		o.EnableFallback = aws.FalseTernary
	}
	return append(append([]func(*imds.Options){}, optFns...), strict)
}

// permissiveOptions returns the caller options followed by an environment
// resolution of the IMDSv1 opt-out: when AWS_EC2_METADATA_V1_DISABLED is set
// to true, fallback is disabled (FalseTernary); otherwise EnableFallback is
// left as-is. The trailing entry wins, so callers cannot accidentally
// re-enable fallback when the operator opted out.
func permissiveOptions(logger *zap.Logger, optFns []func(*imds.Options)) []func(*imds.Options) {
	permissive := func(o *imds.Options) {
		if ec2MetadataV1Disabled(logger) {
			o.EnableFallback = aws.FalseTernary
		}
	}
	return append(append([]func(*imds.Options){}, optFns...), permissive)
}

// ec2MetadataV1Disabled reports whether AWS_EC2_METADATA_V1_DISABLED disables
// IMDSv1 fallback. Parsing mirrors the AWS SDK shared config loader:
// case-insensitive "true"/"false", unset means false. The SDK fails config
// loading on any other value; since no error can be returned here, an invalid
// value is logged (when a logger is available) and treated as unset.
func ec2MetadataV1Disabled(logger *zap.Logger) bool {
	value := os.Getenv(ec2MetadataV1DisabledEnvVar)
	switch {
	case value == "" || strings.EqualFold(value, "false"):
		return false
	case strings.EqualFold(value, "true"):
		return true
	default:
		if logger != nil {
			logger.Warn("invalid value for environment variable, need true or false",
				zap.String(ec2MetadataV1DisabledEnvVar, value))
		}
		return false
	}
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
