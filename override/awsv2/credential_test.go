// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsv2

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	credentialsv1 "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCredentialsChainOverride_Singleton(t *testing.T) {
	resetOverride(t)

	first := GetCredentialsChainOverride()
	second := GetCredentialsChainOverride()
	assert.Same(t, first, second)
}

func TestAppendCredentialsChain_OrderPreserved(t *testing.T) {
	resetOverride(t)

	o := GetCredentialsChainOverride()
	o.AppendCredentialsChain(staticV2Factory("first"))
	o.AppendCredentialsChain(staticV2Factory("second"))
	o.AppendCredentialsChain(staticV2Factory("third"))

	chain := o.GetCredentialsChain()
	require.Len(t, chain, 3)

	for i, want := range []string{"first", "second", "third"} {
		creds, err := chain[i]("any").Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, want, creds.AccessKeyID)
	}
}

func TestGetCredentialsChain_ReturnsCopy(t *testing.T) {
	resetOverride(t)

	o := GetCredentialsChainOverride()
	o.AppendCredentialsChain(staticV2Factory("only"))

	chain := o.GetCredentialsChain()
	chain[0] = staticV2Factory("clobbered") // mutate the returned slice

	internal := o.GetCredentialsChain()
	creds, err := internal[0]("any").Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "only", creds.AccessKeyID, "registry must not be affected by callers mutating the returned slice")
}

func TestAppendCredentialsChain_NilFactoryReturnPropagates(t *testing.T) {
	resetOverride(t)

	o := GetCredentialsChainOverride()
	// Factory returns nil for paths it doesn't handle. The registry stores the factory as-is.
	// Downstream chain consumers must filter nil. This test pins that behavior.
	o.AppendCredentialsChain(func(filename string) aws.CredentialsProvider {
		if filename == "match" {
			return staticV2Provider("got-match")
		}
		return nil
	})

	chain := o.GetCredentialsChain()
	require.Len(t, chain, 1)
	assert.Nil(t, chain[0]("nope"))
	assert.NotNil(t, chain[0]("match"))
}

func TestAppendCredentialsChain_ConcurrentRegistration(t *testing.T) {
	resetOverride(t)

	const n = 50
	o := GetCredentialsChainOverride()

	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			o.AppendCredentialsChain(staticV2Factory("concurrent"))
		}()
	}
	wg.Wait()

	assert.Len(t, o.GetCredentialsChain(), n,
		"all concurrent registrations must persist; a missing entry indicates a lock bug")
}

func TestNewV1ProviderAdapter_Nil(t *testing.T) {
	assert.Nil(t, NewV1ProviderAdapter(nil), "nil v1 provider should yield nil v2 provider")
}

func TestNewV1ProviderAdapter_Static(t *testing.T) {
	v2Provider := NewV1ProviderAdapter(&staticV1Provider{
		value: credentialsv1.Value{
			AccessKeyID:     "v1-static",
			SecretAccessKey: "secret",
			SessionToken:    "session-token",
			ProviderName:    "test",
		},
	})
	require.NotNil(t, v2Provider)

	creds, err := v2Provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "v1-static", creds.AccessKeyID)
	assert.Equal(t, "secret", creds.SecretAccessKey)
	assert.Equal(t, "session-token", creds.SessionToken)
	assert.Equal(t, "test", creds.Source)
	assert.False(t, creds.CanExpire)
}

func TestNewV1ProviderAdapter_Expiring(t *testing.T) {
	expiry := time.Now().Add(15 * time.Minute)
	v2Provider := NewV1ProviderAdapter(&expiringV1Provider{
		value:  credentialsv1.Value{AccessKeyID: "v1-expiring", SecretAccessKey: "secret"},
		expiry: expiry,
	})

	creds, err := v2Provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.True(t, creds.CanExpire)
	assert.Equal(t, expiry, creds.Expires)
}

func TestNewV1ProviderAdapter_ZeroExpiresAt(t *testing.T) {
	v2Provider := NewV1ProviderAdapter(&expiringV1Provider{
		value: credentialsv1.Value{AccessKeyID: "zero-exp"},
		// expiry left as zero value
	})

	creds, err := v2Provider.Retrieve(t.Context())
	require.NoError(t, err)
	assert.False(t, creds.CanExpire, "zero ExpiresAt should not enable expiration")
	assert.True(t, creds.Expires.IsZero())
}

func TestNewV1ProviderAdapter_RetrieveError(t *testing.T) {
	v2Provider := NewV1ProviderAdapter(&staticV1Provider{err: errors.New("v1 failure")})
	_, err := v2Provider.Retrieve(t.Context())
	assert.ErrorContains(t, err, "v1 failure")
}

// resetOverride clears the singleton so each test starts fresh. Tests in this package mutate process-global state,
// so they cannot run in parallel with each other.
func resetOverride(t *testing.T) {
	t.Helper()
	credentialsChainOverride = nil
	credentialsChainOverrideOnce = sync.Once{}
}

func staticV2Provider(accessKey string) aws.CredentialsProvider {
	return aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: accessKey, SecretAccessKey: "secret"}, nil
	})
}

func staticV2Factory(accessKey string) CredentialsProviderFactory {
	return func(string) aws.CredentialsProvider { return staticV2Provider(accessKey) }
}

// staticV1Provider returns a fixed value/error and has no expiration semantics.
type staticV1Provider struct {
	value credentialsv1.Value
	err   error
}

func (s *staticV1Provider) Retrieve() (credentialsv1.Value, error) {
	return s.value, s.err
}

func (s *staticV1Provider) IsExpired() bool {
	return false
}

// expiringV1Provider satisfies credentialsv1.Expirer to mirror v1 providers that embed credentials.Expiry.
type expiringV1Provider struct {
	value  credentialsv1.Value
	expiry time.Time
}

func (e *expiringV1Provider) Retrieve() (credentialsv1.Value, error) {
	return e.value, nil
}

func (e *expiringV1Provider) IsExpired() bool {
	return time.Now().After(e.expiry)
}

func (e *expiringV1Provider) ExpiresAt() time.Time {
	return e.expiry
}
