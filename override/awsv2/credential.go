// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsv2 // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	credentialsv1 "github.com/aws/aws-sdk-go/aws/credentials"
)

// CredentialsProviderFactory creates an aws.CredentialsProvider for the given shared credentials file path.
type CredentialsProviderFactory func(file string) aws.CredentialsProvider

// CredentialsChainOverride is a process-global registry of CredentialsProviderFactory functions.
// External packages register custom factories via AppendCredentialsChain in init().
type CredentialsChainOverride struct {
	mu        sync.Mutex
	factories []CredentialsProviderFactory
}

var (
	credentialsChainOverride     *CredentialsChainOverride
	credentialsChainOverrideOnce sync.Once
)

// GetCredentialsChainOverride returns the process-global registry.
func GetCredentialsChainOverride() *CredentialsChainOverride {
	credentialsChainOverrideOnce.Do(func() {
		credentialsChainOverride = &CredentialsChainOverride{}
	})
	return credentialsChainOverride
}

// AppendCredentialsChain registers a factory. For a v1 SDK provider, compose with NewV1ProviderAdapter:
//
//	override.AppendCredentialsChain(func(file string) aws.CredentialsProvider {
//	    return awsv2.NewV1ProviderAdapter(myV1Factory(file))
//	})
func (c *CredentialsChainOverride) AppendCredentialsChain(factory CredentialsProviderFactory) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.factories = append(c.factories, factory)
}

// GetCredentialsChain returns a snapshot of the registered factories, so callers may iterate without locking.
func (c *CredentialsChainOverride) GetCredentialsChain() []CredentialsProviderFactory {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CredentialsProviderFactory(nil), c.factories...)
}

// NewV1ProviderAdapter wraps a v1 SDK credentials.Provider as an aws.CredentialsProvider, or returns
// nil if the provider is nil.
//
// To convey expiration, the provider must implement credentialsv1.Expirer. Most v1 providers do via embedded
// credentials.Expiry. Without it, the v2 CredentialsCache treats the credentials as never-expiring.
//
// v1's Provider.Retrieve takes no context, so the ctx passed to v2 Retrieve does not propagate.
func NewV1ProviderAdapter(p credentialsv1.Provider) aws.CredentialsProvider {
	if p == nil {
		return nil
	}
	return &v1ProviderAdapter{provider: p}
}

// v1ProviderAdapter implements aws.CredentialsProvider for a v1 credentials.Provider.
type v1ProviderAdapter struct {
	provider credentialsv1.Provider
}

var _ aws.CredentialsProvider = (*v1ProviderAdapter)(nil)

func (a *v1ProviderAdapter) Retrieve(_ context.Context) (aws.Credentials, error) {
	val, err := a.provider.Retrieve()
	if err != nil {
		return aws.Credentials{}, err
	}
	creds := aws.Credentials{
		AccessKeyID:     val.AccessKeyID,
		SecretAccessKey: val.SecretAccessKey,
		SessionToken:    val.SessionToken,
		Source:          val.ProviderName,
	}
	// Honor v1 expiration via credentialsv1.Expirer (commonly satisfied via embedded credentials.Expiry).
	if expirer, ok := a.provider.(credentialsv1.Expirer); ok {
		if exp := expirer.ExpiresAt(); !exp.IsZero() {
			creds.CanExpire = true
			creds.Expires = exp
		}
	}
	return creds, nil
}
