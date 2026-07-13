// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultAzureIMDSEndpoint = "http://169.254.169.254/metadata/identity/oauth2/token"
	azureIMDSInstancePath    = "/metadata/instance"
	// azureIMDSAPIVersion is shared by both the token and instance-probe
	// requests. IMDS versions its whole supported-versions list service-wide
	// (not per-endpoint): 2020-09-01 satisfies the token endpoint's documented
	// "2018-02-01 or greater" floor and is a supported instance version. It also
	// matches internal/metadataproviders/azure.
	azureIMDSAPIVersion     = "2020-09-01"
	defaultAzureResource    = "https://management.azure.com/"
	defaultAzureTokenExpiry = 3600
	// azureIMDSProbeTimeout bounds the availability probe so a blackholed
	// link-local address cannot stall extension startup for the full
	// token-fetch timeout.
	azureIMDSProbeTimeout = 3 * time.Second
)

type azureProvider struct {
	client   *http.Client
	endpoint string
	resource string
}

var _ TokenProvider = (*azureProvider)(nil)

func newAzureProvider(resource string) *azureProvider {
	if resource == "" {
		resource = defaultAzureResource
	}
	// IMDS lives at a fixed link-local address; never route metadata requests
	// through an HTTP(S) proxy. Clone the default transport for sane dial/TLS
	// defaults, then disable proxy resolution.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &azureProvider{
		client:   &http.Client{Timeout: 30 * time.Second, Transport: transport},
		endpoint: defaultAzureIMDSEndpoint,
		resource: resource,
	}
}

func (*azureProvider) Name() string { return "azure" }

// instanceMetadataURL derives the availability-probe URL from the same base
// (scheme + host) as the token endpoint, so endpoint overrides apply to both.
func (p *azureProvider) instanceMetadataURL() string {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return ""
	}
	u.Path = azureIMDSInstancePath
	u.RawQuery = url.Values{"api-version": {azureIMDSAPIVersion}}.Encode()
	return u.String()
}

func (p *azureProvider) IsAvailable(ctx context.Context) bool {
	// Use a short, independent timeout for the probe so a non-Azure host with a
	// blackholed IMDS address does not block startup for the token-fetch timeout.
	ctx, cancel := context.WithTimeout(ctx, azureIMDSProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.instanceMetadataURL(), http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("Metadata", "true")
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type azureTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   string `json:"expires_in"`
}

func (p *azureProvider) GetToken(ctx context.Context) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, http.NoBody)
	if err != nil {
		return "", 0, fmt.Errorf("azure: create request: %w", err)
	}
	req.Header.Set("Metadata", "true")
	q := req.URL.Query()
	q.Set("api-version", azureIMDSAPIVersion)
	q.Set("resource", p.resource)
	req.URL.RawQuery = q.Encode()

	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("azure: IMDS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("azure: IMDS returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp azureTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", 0, fmt.Errorf("azure: decode response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", 0, errors.New("azure: empty access_token in IMDS response")
	}

	expiresIn, _ := strconv.Atoi(tokenResp.ExpiresIn)
	if expiresIn <= 0 {
		expiresIn = defaultAzureTokenExpiry
	}
	return tokenResp.AccessToken, time.Duration(expiresIn) * time.Second, nil
}
