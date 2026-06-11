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
	"strconv"
	"time"
)

const (
	defaultAzureIMDSEndpoint   = "http://169.254.169.254/metadata/identity/oauth2/token"
	defaultAzureIMDSAPIVersion = "2018-02-01"
	defaultAzureResource       = "https://management.azure.com/"
	defaultAzureTokenExpiry    = 3600
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
	return &azureProvider{
		client:   &http.Client{Timeout: 30 * time.Second},
		endpoint: defaultAzureIMDSEndpoint,
		resource: resource,
	}
}

func (*azureProvider) Name() string { return "azure" }

func (p *azureProvider) IsAvailable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://169.254.169.254/metadata/instance?api-version=2021-02-01", http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("Metadata", "true")
	resp, err := p.client.Do(req) //nolint:gosec // IMDS is a fixed local endpoint
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type azureTokenResponse struct {
	AccessToken string `json:"access_token"` //nolint:gosec // JSON field name, not a secret
	ExpiresIn   string `json:"expires_in"`
}

func (p *azureProvider) GetToken(ctx context.Context) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, http.NoBody)
	if err != nil {
		return "", 0, fmt.Errorf("azure: create request: %w", err)
	}
	req.Header.Set("Metadata", "true")
	q := req.URL.Query()
	q.Set("api-version", defaultAzureIMDSAPIVersion)
	q.Set("resource", p.resource)
	req.URL.RawQuery = q.Encode()

	resp, err := p.client.Do(req) //nolint:gosec // endpoint is IMDS, not user-controlled
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
