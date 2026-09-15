// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package aws // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"

import (
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// CredentialsChainOverride is a process-global extension point that
// lets external callers register additional aws.CredentialsProvider
// factories ahead of the default credential resolution path used by
// internal/aws/awsutil.
type CredentialsChainOverride struct {
	mu        sync.Mutex
	factories []func(string) aws.CredentialsProvider
}

var (
	credentialsChainOverride     *CredentialsChainOverride
	credentialsChainOverrideOnce sync.Once
)

// GetCredentialsChainOverride returns the process-global singleton.
func GetCredentialsChainOverride() *CredentialsChainOverride {
	credentialsChainOverrideOnce.Do(func() {
		credentialsChainOverride = &CredentialsChainOverride{}
	})
	return credentialsChainOverride
}

// AppendCredentialsChain registers a credentials-provider factory. The
// factory is invoked once per entry in the consumer's
// shared-credentials file list and may return nil to decline.
// Safe for concurrent use; registration is expected at init() time,
// before any consumer reads the chain.
func (c *CredentialsChainOverride) AppendCredentialsChain(factory func(string) aws.CredentialsProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.factories = append(c.factories, factory)
}

// GetCredentialsChain returns a snapshot copy of the registered
// factories. Callers may iterate and mutate the returned slice freely;
// it is decoupled from the registry's internal state.
func (c *CredentialsChainOverride) GetCredentialsChain() []func(string) aws.CredentialsProvider {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]func(string) aws.CredentialsProvider(nil), c.factories...)
}
