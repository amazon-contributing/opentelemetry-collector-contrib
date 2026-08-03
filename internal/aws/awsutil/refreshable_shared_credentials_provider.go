// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"context"
	"fmt"
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

// RefreshableSharedCredentialsProvider stamps an expiry on credentials
// retrieved from Provider so the SDK's credentials cache will re-read
// the underlying file periodically and pick up rotated values without
// an agent restart.
type RefreshableSharedCredentialsProvider struct {
	Provider     SharedCredentialsProvider
	ExpiryWindow time.Duration // Zero means defaultExpiryWindow.
}

var _ aws.CredentialsProvider = (*RefreshableSharedCredentialsProvider)(nil)

func (p RefreshableSharedCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	creds, err := p.Provider.Retrieve(ctx)
	if err != nil {
		return aws.Credentials{}, err
	}
	window := p.ExpiryWindow
	if window == 0 {
		window = defaultExpiryWindow
	}
	creds.CanExpire = true
	creds.Expires = time.Now().Add(window)
	return creds, nil
}

// SharedCredentialsProvider loads credentials from a shared-credentials
// file and profile. An empty Filename uses the SDK's default
// shared-credentials file resolution. An empty Profile resolves to the
// AWS_PROFILE environment variable when set, otherwise "default": the v2
// SDK's LoadSharedConfigProfile rejects an empty profile name, so passing
// it through unchanged would fail whenever a caller sets a credentials
// file without an explicit profile.
type SharedCredentialsProvider struct {
	Filename string
	Profile  string
}

var _ aws.CredentialsProvider = (*SharedCredentialsProvider)(nil)

func (p SharedCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	profile := p.Profile
	if profile == "" {
		profile = os.Getenv(envAwsProfile)
	}
	if profile == "" {
		profile = defaultProfileName
	}
	filename := p.Filename
	if filename == "" {
		setFromEnvVal(&filename, envAwsSharedCredentialsFile)
	}
	if filename == "" {
		filename = defaultSharedCredentialsFile(backwardsCompatibleUserHomeDir())
	}
	opts := []func(*config.LoadSharedConfigOptions){func(o *config.LoadSharedConfigOptions) {
		// Read credentials only from the resolved file. Empty ConfigFiles
		// prevents the SDK from also merging the default shared config file
		// (for example $HOME/.aws/config), so the credentials file is
		// authoritative and a missing file or profile fails loudly instead
		// of silently resolving elsewhere.
		o.CredentialsFiles = []string{filename}
		o.ConfigFiles = []string{}
	}}
	sharedConfig, err := config.LoadSharedConfigProfile(ctx, profile, opts...)
	if err != nil {
		return aws.Credentials{}, err
	}
	if !sharedConfig.Credentials.HasKeys() {
		return aws.Credentials{}, fmt.Errorf("shared credentials profile %q in %q does not contain static credentials", profile, filename)
	}
	return sharedConfig.Credentials, nil
}
