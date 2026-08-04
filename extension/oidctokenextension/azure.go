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
	"strings"
	"sync"
	"time"
)

const (
	defaultAzureIMDSEndpoint = "http://169.254.169.254/metadata/identity/oauth2/token"
	azureIMDSInstancePath    = "/metadata/instance"
	// azureIMDSAPIVersion is shared by the token, instance-probe, and
	// azEnvironment requests. IMDS versions its whole supported-versions list
	// service-wide (not per-endpoint): 2020-09-01 satisfies the token
	// endpoint's documented "2018-02-01 or greater" floor and is a supported
	// instance version. It also matches internal/metadataproviders/azure.
	azureIMDSAPIVersion     = "2020-09-01"
	defaultAzureTokenExpiry = 3600
	// azureIMDSProbeTimeout bounds the availability probe so a blackholed
	// link-local address cannot stall extension startup for the full
	// token-fetch timeout.
	azureIMDSProbeTimeout = 3 * time.Second
)

// Azure Resource Manager (ARM) resource identifiers per sovereign cloud. The
// managed-identity OIDC token's audience must target ARM for the cloud the VM
// runs in, and the AWS IAM OIDC trust policy's :aud condition must match the
// same value for AssumeRoleWithWebIdentity to succeed.
const (
	armResourcePublic = "https://management.azure.com/"
	armResourceChina  = "https://management.chinacloudapi.cn/"
	armResourceUSGov  = "https://management.usgovcloudapi.net/"
	// defaultAzureResource is the public-cloud ARM resource, used as the
	// fallback when the cloud cannot be detected.
	defaultAzureResource = armResourcePublic
)

// armResourceForEnvironment maps an IMDS compute.azEnvironment value to its ARM
// resource. Comparison is case-insensitive because IMDS returns mixed case
// ("AzureChinaCloud"). Unknown or empty environments fall back to public ARM,
// which preserves the previous behavior.
func armResourceForEnvironment(env string) string {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "azurechinacloud":
		return armResourceChina
	case "azureusgovernmentcloud", "azureusgovernment":
		return armResourceUSGov
	default:
		return armResourcePublic
	}
}

type azureProvider struct {
	client   *http.Client
	endpoint string
	// configuredResource is the explicit audience override from config. When
	// empty, the ARM resource is auto-detected from the VM's Azure cloud.
	configuredResource string

	mu               sync.Mutex
	resolvedResource string
}

var _ TokenProvider = (*azureProvider)(nil)

func newAzureProvider(resource string) *azureProvider {
	// IMDS lives at a fixed link-local address; never route metadata requests
	// through an HTTP(S) proxy. Clone the default transport for sane dial/TLS
	// defaults, then disable proxy resolution.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &azureProvider{
		client:             &http.Client{Timeout: 30 * time.Second, Transport: transport},
		endpoint:           defaultAzureIMDSEndpoint,
		configuredResource: resource,
	}
}

func (*azureProvider) Name() string { return "azure" }

// instanceMetadataURL derives an instance-metadata URL from the same base
// (scheme + host) as the token endpoint, so endpoint overrides apply to both.
// An optional leaf (e.g. "/compute/azEnvironment") is appended to the instance
// path, and extra query params are merged in.
func (p *azureProvider) instanceMetadataURL(leaf string, extra url.Values) string {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return ""
	}
	u.Path = azureIMDSInstancePath + leaf
	q := url.Values{"api-version": {azureIMDSAPIVersion}}
	for k, vs := range extra {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// resolveResource returns the ARM resource to request. An explicit configured
// audience always wins. Otherwise it is detected once from azEnvironment and
// cached; a detection failure returns the public-cloud fallback without
// caching, so a later refresh can retry once IMDS is reachable.
func (p *azureProvider) resolveResource(ctx context.Context) string {
	if p.configuredResource != "" {
		return p.configuredResource
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resolvedResource != "" {
		return p.resolvedResource
	}
	env, err := p.detectEnvironment(ctx)
	resource := armResourceForEnvironment(env)
	if err != nil {
		return resource // fallback for this call only; do not cache
	}
	p.resolvedResource = resource
	return resource
}

// detectEnvironment reads compute.azEnvironment from IMDS instance metadata.
func (p *azureProvider) detectEnvironment(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.instanceMetadataURL("/compute/azEnvironment", url.Values{"format": {"text"}}), http.NoBody)
	if err != nil {
		return "", fmt.Errorf("azure: create azEnvironment request: %w", err)
	}
	req.Header.Set("Metadata", "true")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("azure: azEnvironment request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("azure: azEnvironment returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("azure: read azEnvironment: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}

func (p *azureProvider) IsAvailable(ctx context.Context) bool {
	// Use a short, independent timeout for the probe so a non-Azure host with a
	// blackholed IMDS address does not block startup for the token-fetch timeout.
	ctx, cancel := context.WithTimeout(ctx, azureIMDSProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.instanceMetadataURL("", nil), http.NoBody)
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
	q.Set("resource", p.resolveResource(ctx))
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
