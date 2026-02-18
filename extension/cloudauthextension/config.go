// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/cloudauthextension"

import (
	"go.opentelemetry.io/collector/component"
)

// Config defines the configuration for the cloud auth extension.
type Config struct {
	// TokenFile is a path to a file containing an OIDC/JWT token. When set,
	// the extension reads the token from this file instead of auto-detecting
	// a cloud provider. The user is responsible for keeping the file current.
	TokenFile string `mapstructure:"token_file,omitempty"`

	// Audience is the audience/resource claim requested in the OIDC token.
	// Defaults to "https://management.azure.com/" for Azure auto-detection.
	Audience string `mapstructure:"audience,omitempty"`

	// TokenDir is the directory where the extension writes the fetched OIDC
	// token file. Defaults to os.TempDir().
	TokenDir string `mapstructure:"token_dir,omitempty"`
}

var _ component.Config = (*Config)(nil)
