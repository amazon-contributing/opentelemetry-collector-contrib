// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	override "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// overrideChainContribution mirrors the inner double loop in
// getCredentialProviderChain so chain-length tests can assert deltas
// against whatever override-chain state the process happens to be in.
// override/aws's singleton has no public reset, so this is the cleanest
// way to write tests that don't depend on prior init() state.
func overrideChainContribution(files []string) int {
	n := 0
	for _, factory := range override.GetCredentialsChainOverride().GetCredentialsChain() {
		for _, file := range files {
			if factory(file) != nil {
				n++
			}
		}
	}
	return n
}

func TestGetCredentialProviderChain_Empty(t *testing.T) {
	// Independent of override state because SharedCredentialsFile is empty.
	cfg := &AWSSessionSettings{}
	chain := getCredentialProviderChain(cfg)
	assert.Empty(t, chain)
}

func TestGetCredentialProviderChain_ProfileOnly(t *testing.T) {
	// One entry: the profile-only fallback. Independent of override state
	// because SharedCredentialsFile is empty.
	cfg := &AWSSessionSettings{Profile: "myprofile"}
	chain := getCredentialProviderChain(cfg)
	require.Len(t, chain, 1)
}

func TestGetCredentialProviderChain_SharedCredentialsFiles(t *testing.T) {
	files := []string{"/tmp/file1", "/tmp/file2"}
	cfg := &AWSSessionSettings{
		Profile:               "myprofile",
		SharedCredentialsFile: files,
	}
	chain := getCredentialProviderChain(cfg)
	want := overrideChainContribution(files) + len(files)
	require.Len(t, chain, want)
}

func TestGetCredentialProviderChain_FilesWithoutProfile(t *testing.T) {
	files := []string{"/tmp/file1"}
	cfg := &AWSSessionSettings{SharedCredentialsFile: files}
	chain := getCredentialProviderChain(cfg)
	want := overrideChainContribution(files) + len(files)
	require.Len(t, chain, want)
}

func TestGetRootCredentials_FirstNonNil(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		assert.Nil(t, getRootCredentials(&AWSSessionSettings{}))
	})

	t.Run("ProfileSet", func(t *testing.T) {
		got := getRootCredentials(&AWSSessionSettings{Profile: "p"})
		assert.NotNil(t, got)
	})
}

func TestLoadConfigWithRetry_ContextCanceledDuringWait(t *testing.T) {
	failingLoad := func(context.Context, ...func(*config.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, assert.AnError
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := loadConfigWithRetry(ctx, zap.NewNop(), failingLoad, nil, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 10*time.Second, "cancellation must interrupt the retry wait promptly")
}

// contractStubProvider is a natively-v2 stub returned by the fake
// override factory registered in
// TestGetCredentialProviderChain_OverrideConsumerContract.
type contractStubProvider struct {
	filename string
}

func (s contractStubProvider) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:  "contract-stub",
		SessionToken: s.filename,
		Source:       "contract-stub",
	}, nil
}

func TestGetCredentialProviderChain_OverrideConsumerContract(t *testing.T) {
	// The override registry is a process-global singleton with no reset
	// mechanism, so this test tolerates pre-registered factories: it
	// identifies its own contributions by provider type and only asserts
	// deltas and relative ordering. The prefix is unique per run so
	// factories registered by earlier runs (-count=N) decline all of
	// this run's filenames.
	acceptPrefix := fmt.Sprintf("/override-contract-accept-%d/", time.Now().UnixNano())
	files := []string{
		acceptPrefix + "creds-a",
		"/override-contract-decline/creds-b",
		acceptPrefix + "creds-c",
	}

	var invoked []string
	override.GetCredentialsChainOverride().AppendCredentialsChain(
		func(filename string) aws.CredentialsProvider {
			invoked = append(invoked, filename)
			if strings.HasPrefix(filename, acceptPrefix) {
				return contractStubProvider{filename: filename}
			}
			return nil
		},
	)

	invoked = nil
	chain := getCredentialProviderChain(&AWSSessionSettings{SharedCredentialsFile: files})

	// The factory is invoked exactly once per SharedCredentialsFile entry,
	// in order.
	assert.Equal(t, files, invoked)

	// The default chain contributes one *aws.CredentialsCache entry per
	// file, appended after all override contributions.
	require.GreaterOrEqual(t, len(chain), len(files))
	defaults := chain[len(chain)-len(files):]
	for i, p := range defaults {
		assert.IsTypef(t, &aws.CredentialsCache{}, p, "trailing entry %d must be a default chain entry", i)
	}

	// Non-nil factory returns are placed ahead of the default entries;
	// nil returns are skipped (no entry for the declined filename).
	overridePortion := chain[:len(chain)-len(files)]
	var mine []string
	for _, p := range overridePortion {
		if stub, ok := p.(contractStubProvider); ok {
			mine = append(mine, stub.filename)
		}
	}
	assert.Equal(t, []string{acceptPrefix + "creds-a", acceptPrefix + "creds-c"}, mine)

	for _, p := range chain {
		require.NotNil(t, p)
	}
}
