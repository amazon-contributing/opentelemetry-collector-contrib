// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package gcp // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension/internal/provider/gcp"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension/internal/provider"
)

const (
	// defaultMetadataHost is the GCE metadata server base URL. The token and probe paths are joined onto it,
	// so overriding it (in tests) redirects both.
	defaultMetadataHost = "http://metadata.google.internal"
	// identityPath is the service-account identity endpoint. It returns a Google-signed OIDC JWT (issuer
	// https://accounts.google.com) for the instance's default service account.
	identityPath = "/computeMetadata/v1/instance/service-accounts/default/identity"
	// instanceIDPath is a stable leaf used only for the availability probe. Every GCE instance exposes it.
	instanceIDPath = "/computeMetadata/v1/instance/id"
	// defaultAudience is the audience requested in the identity token. It is effectively cosmetic: GCE tokens
	// also carry an azp claim (the service account's unique ID), and AWS STS uses azp as the audience. The
	// metadata endpoint just requires a non-empty value.
	defaultAudience = "sts.amazonaws.com"
	// defaultTokenExpiry is the fallback TTL used when the token's exp claim cannot be parsed. GCE identity
	// tokens are normally valid 1 hour.
	defaultTokenExpiry = time.Hour
	// The GCE metadata server requires this request header and echoes it back on responses, which is how a
	// host is confirmed to be GCE.
	metadataFlavorHeader = "Metadata-Flavor"
	metadataFlavorValue  = "Google"
)

// gcpProvider fetches an OIDC token from the GCE metadata server.
type gcpProvider struct {
	client   *http.Client
	host     string
	audience string
}

var _ provider.TokenProvider = (*gcpProvider)(nil)

// New returns a GCE metadata-server token provider. An empty audience defaults to sts.amazonaws.com.
func New(audience string) provider.TokenProvider {
	if audience == "" {
		audience = defaultAudience
	}
	return &gcpProvider{
		client:   provider.NewMetadataClient(),
		host:     defaultMetadataHost,
		audience: audience,
	}
}

func (*gcpProvider) Name() string { return "gcp" }

// IsAvailable reports whether the host is a GCE instance by probing the metadata server: a 200 with a
// Metadata-Flavor: Google response header identifies it. Best-effort detection for provider selection, not
// an authenticated check.
func (p *gcpProvider) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, provider.DefaultMetadataProbeTimeout)
	defer cancel()

	probeURL, err := url.JoinPath(p.host, instanceIDPath)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, http.NoBody)
	if err != nil {
		return false
	}
	req.Header.Set(metadataFlavorHeader, metadataFlavorValue)
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK && resp.Header.Get(metadataFlavorHeader) == metadataFlavorValue
}

func (p *gcpProvider) GetToken(ctx context.Context) (string, time.Duration, error) {
	tokenURL, err := url.JoinPath(p.host, identityPath)
	if err != nil {
		return "", 0, fmt.Errorf("gcp: build token url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, http.NoBody)
	if err != nil {
		return "", 0, fmt.Errorf("gcp: create request: %w", err)
	}
	req.Header.Set(metadataFlavorHeader, metadataFlavorValue)
	q := req.URL.Query()
	q.Set("audience", p.audience)
	// format=standard yields a standard OIDC ID token (aud/azp/exp/iat/iss/sub), which is all STS needs.
	q.Set("format", "standard")
	req.URL.RawQuery = q.Encode()

	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("gcp: metadata request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("gcp: metadata returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("gcp: read response: %w", err)
	}
	// The identity endpoint returns the raw JWT as the response body.
	token := strings.TrimSpace(string(body))
	if token == "" {
		return "", 0, errors.New("gcp: empty identity token in metadata response")
	}
	return token, tokenTTL(token), nil
}

// tokenTTL derives the token lifetime from the JWT's exp claim, falling back to defaultTokenExpiry when the
// token cannot be parsed.
func tokenTTL(token string) time.Duration {
	fallback := defaultTokenExpiry
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fallback
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fallback
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err = json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return fallback
	}
	ttl := time.Until(time.Unix(claims.Exp, 0))
	if ttl <= 0 {
		return fallback
	}
	return ttl
}
