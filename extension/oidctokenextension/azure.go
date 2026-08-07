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
	"sync/atomic"
	"time"
)

const (
	defaultAzureIMDSEndpoint = "http://169.254.169.254/metadata/identity/oauth2/token"
	azureIMDSInstancePath    = "/metadata/instance"
	// azureIMDSAPIVersion is shared by the token and instance-probe requests.
	// IMDS versions its whole supported-versions list service-wide (not
	// per-endpoint): 2020-09-01 satisfies the token endpoint's documented
	// "2018-02-01 or greater" floor and is a supported instance version. It also
	// matches internal/metadataproviders/azure.
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
	// empty, the ARM resource is auto-detected from the VM's Azure cloud during
	// the availability probe.
	configuredResource string
	// resolvedResource caches the ARM resource detected from compute.azEnvironment
	// by IsAvailable. It is read from GetToken, which may run on a different
	// goroutine than the probe, so access is atomic.
	resolvedResource atomic.Value // string
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

// IsAvailable probes IMDS for compute.azEnvironment. A 200 both confirms the
// host is an Azure VM and yields the cloud environment, from which the ARM
// resource (the OIDC token audience) is derived and cached for GetToken. This
// folds availability and environment detection into a single bounded probe, so
// no separate detection call (and no lock around it) is needed on the token path.
func (p *azureProvider) IsAvailable(ctx context.Context) bool {
	// Use a short, independent timeout for the probe so a non-Azure host with a
	// blackholed IMDS address does not block startup for the token-fetch timeout.
	ctx, cancel := context.WithTimeout(ctx, azureIMDSProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.instanceMetadataURL("/compute/azEnvironment", url.Values{"format": {"text"}}), http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set("Metadata", "true")
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	p.resolvedResource.Store(armResourceForEnvironment(string(body)))
	return true
}

// resource returns the ARM resource for the token audience: the explicit
// configured override if set, otherwise the value cached by IsAvailable, else
// the public-cloud fallback.
func (p *azureProvider) resource() string {
	if p.configuredResource != "" {
		return p.configuredResource
	}
	if v, ok := p.resolvedResource.Load().(string); ok && v != "" {
		return v
	}
	return armResourcePublic
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
	q.Set("resource", p.resource())
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
