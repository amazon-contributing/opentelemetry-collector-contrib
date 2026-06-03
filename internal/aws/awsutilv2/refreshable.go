// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

const (
	defaultExpiryWindow = 10 * time.Minute
	defaultProfileName  = "default"
	envAwsProfile       = "AWS_PROFILE"
)

type sharedCredentialsProvider struct {
	filename string
	profile  string
}

var _ aws.CredentialsProvider = (*sharedCredentialsProvider)(nil)

// newSharedCredentialsProvider creates a credentials provider that loads from a shared credentials
// file and profile. An empty filename uses the SDK default shared-credentials file resolution.
// An empty profile resolves to AWS_PROFILE if set, otherwise "default".
func newSharedCredentialsProvider(filename, profile string) aws.CredentialsProvider {
	if profile == "" {
		profile = os.Getenv(envAwsProfile)
	}
	if profile == "" {
		profile = defaultProfileName
	}
	return &sharedCredentialsProvider{filename: filename, profile: profile}
}

func (p *sharedCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	var opts []func(*config.LoadSharedConfigOptions)
	if p.filename != "" {
		opts = append(opts, func(o *config.LoadSharedConfigOptions) {
			// Read credentials only from the caller's file. Empty ConfigFiles prevents
			// the SDK from consulting $HOME/.aws/config.
			o.CredentialsFiles = []string{p.filename}
			o.ConfigFiles = []string{}
		})
	}
	sharedConfig, err := config.LoadSharedConfigProfile(ctx, p.profile, opts...)
	if err != nil {
		return aws.Credentials{}, err
	}
	return sharedConfig.Credentials, nil
}

type refreshableSharedCredentialsProvider struct {
	provider     aws.CredentialsProvider
	expiryWindow time.Duration
}

var _ aws.CredentialsProvider = (*refreshableSharedCredentialsProvider)(nil)

// NewRefreshableSharedCredentialsProvider wraps newSharedCredentialsProvider with periodic re-reads.
// expiryWindow controls how often credentials are refreshed.
func NewRefreshableSharedCredentialsProvider(filename, profile string, expiryWindow time.Duration) aws.CredentialsProvider {
	return &refreshableSharedCredentialsProvider{
		provider:     newSharedCredentialsProvider(filename, profile),
		expiryWindow: expiryWindow,
	}
}

func (p *refreshableSharedCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	creds, err := p.provider.Retrieve(ctx)
	if err != nil {
		return aws.Credentials{}, err
	}
	creds.CanExpire = true
	creds.Expires = time.Now().Add(p.expiryWindow)
	return creds, nil
}
