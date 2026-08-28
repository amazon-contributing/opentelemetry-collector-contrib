// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package provider // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension/internal/provider"

import (
	"net/http"
	"time"
)

// metadataClientTimeout bounds a single token-fetch request. Availability probes use their own shorter
// per-request timeout via context.
const metadataClientTimeout = 30 * time.Second

// DefaultMetadataProbeTimeout bounds a single availability probe so a non-matching host (where the metadata
// endpoint is unreachable or blackholed) cannot stall extension startup for the full token-fetch timeout.
const DefaultMetadataProbeTimeout = 3 * time.Second

// NewMetadataClient returns an HTTP client for cloud metadata-server requests. Metadata endpoints are reached
// over plain HTTP at fixed internal addresses that must never be routed through an HTTP(S) proxy: Azure IMDS
// at the link-local 169.254.169.254, and the GCE metadata server at the hostname metadata.google.internal
// (which resolves to that same link-local address). The client clones the default transport for sane dial/TLS
// defaults and disables proxy resolution.
func NewMetadataClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Timeout: metadataClientTimeout, Transport: transport}
}
