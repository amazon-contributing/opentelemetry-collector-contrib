// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:generate mdatagen metadata.yaml

// Package oidctokenextension provides OIDC token management for authenticating
// to AWS from non-AWS environments (e.g., Azure VMs). It auto-detects the cloud
// provider, fetches OIDC tokens, and writes them to a file that the sigv4auth
// extension references via its web_identity_token_file setting.
package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"
