// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"

import (
	"context"
	"time"
)

// TokenProvider abstracts fetching an OIDC token from a cloud provider.
type TokenProvider interface {
	// GetToken returns a raw OIDC/JWT token suitable for STS AssumeRoleWithWebIdentity.
	GetToken(ctx context.Context) (token string, expiry time.Duration, err error)
	// IsAvailable probes whether this provider can operate in the current environment.
	IsAvailable(ctx context.Context) bool
	// Name returns a human-readable identifier for logging.
	Name() string
}
