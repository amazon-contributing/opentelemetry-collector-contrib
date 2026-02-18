// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/cloudauthextension"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/metadataproviders/azure"
)

const (
	defaultAzureIMDSEndpoint   = "http://169.254.169.254/metadata/identity/oauth2/token"
	defaultAzureIMDSAPIVersion = "2018-02-01"
	defaultAzureResource       = "https://management.azure.com/"
	defaultAzureTokenExpiry    = 3600 // 1 hour fallback
)

// AzureProvider fetches OIDC tokens from Azure IMDS on VMs with managed identity.
type AzureProvider struct {
	client   *http.Client
	endpoint string
	resource string
	detector azure.Provider
}

var _ TokenProvider = (*AzureProvider)(nil)

func newAzureProvider(resource string) *AzureProvider {
	if resource == "" {
		resource = defaultAzureResource
	}
	return &AzureProvider{
		client:   &http.Client{Timeout: 30 * time.Second},
		endpoint: defaultAzureIMDSEndpoint,
		resource: resource,
		detector: azure.NewProvider(),
	}
}

func (p *AzureProvider) Name() string { return "azure" }

// IsAvailable uses the existing Azure metadata provider to detect Azure.
func (p *AzureProvider) IsAvailable(ctx context.Context) bool {
	_, err := p.detector.Metadata(ctx)
	return err == nil
}

type azureTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   string `json:"expires_in"`
}

func (p *AzureProvider) GetToken(ctx context.Context) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return "", 0, fmt.Errorf("azure: create request: %w", err)
	}
	req.Header.Set("Metadata", "true")
	q := req.URL.Query()
	q.Set("api-version", defaultAzureIMDSAPIVersion)
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
