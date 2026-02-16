// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:generate mdatagen metadata.yaml

// Package cloudauthextension provides OIDC token management for authenticating
// to AWS from non-AWS environments (e.g., Azure VMs). It auto-detects the cloud
// provider, fetches OIDC tokens, writes them to a file, and sets
// AWS_WEB_IDENTITY_TOKEN_FILE for the credential chain.
package cloudauthextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/cloudauthextension"
