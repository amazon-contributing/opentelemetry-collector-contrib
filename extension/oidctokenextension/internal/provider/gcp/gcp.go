// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package gcp // import "github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension/internal/provider/gcp"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/oidctokenextension/internal/provider"
)

const (
	// identityPath is the GCE metadata service-account identity endpoint (relative to the metadata client's
	// computeMetadata/v1/ base). It returns a Google-signed OIDC JWT (issuer https://accounts.google.com) for
	// the instance's default service account.
	identityPath = "instance/service-accounts/default/identity"
	// defaultAudience is the audience requested in the identity token. It is effectively cosmetic: GCE tokens
	// also carry an azp claim (the service account's unique ID), and AWS STS uses azp as the audience. The
	// metadata endpoint just requires a non-empty value.
	defaultAudience = "sts.amazonaws.com"
	// defaultTokenExpiry is the fallback TTL used when the token's exp claim cannot be parsed. GCE identity
	// tokens are normally valid 1 hour.
	defaultTokenExpiry = time.Hour
)

// gcpProvider fetches an OIDC token from the GCE metadata server via the compute/metadata client, which
// handles the metadata host, Metadata-Flavor header, and retries.
type gcpProvider struct {
	client   *metadata.Client
	audience string
}

var _ provider.TokenProvider = (*gcpProvider)(nil)

// New returns a GCE metadata-server token provider using the given metadata HTTP client. An empty audience
// defaults to sts.amazonaws.com.
func New(client *http.Client, audience string) provider.TokenProvider {
	if audience == "" {
		audience = defaultAudience
	}
	return &gcpProvider{
		client:   metadata.NewClient(client),
		audience: audience,
	}
}

func (*gcpProvider) Name() string { return "gcp" }

// IsAvailable reports whether the host is a GCE instance. Best-effort detection for provider selection.
func (*gcpProvider) IsAvailable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, provider.DefaultMetadataProbeTimeout)
	defer cancel()
	return metadata.OnGCEWithContext(ctx)
}

func (p *gcpProvider) GetToken(ctx context.Context) (string, time.Duration, error) {
	// format=standard yields a standard OIDC ID token (aud/azp/exp/iat/iss/sub), which is all STS needs.
	suffix := identityPath + "?audience=" + url.QueryEscape(p.audience) + "&format=standard"
	token, err := p.client.GetWithContext(ctx, suffix)
	if err != nil {
		return "", 0, fmt.Errorf("gcp: fetch identity token: %w", err)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", 0, errors.New("gcp: empty identity token in metadata response")
	}
	return token, tokenTTL(token), nil
}

// tokenTTL returns the token lifetime from the JWT's exp claim, falling back to defaultTokenExpiry when exp
// cannot be read (malformed JWT or no exp claim), matching the azure provider's fallback for a missing
// expires_in. An already-expired token yields a non-positive TTL, which triggers an immediate refresh.
func tokenTTL(token string) time.Duration {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return defaultTokenExpiry
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return defaultTokenExpiry
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err = json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return defaultTokenExpiry
	}
	return time.Until(time.Unix(claims.Exp, 0))
}
