// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package gcp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeJWT builds a minimal unsigned JWT with the given exp claim (Unix seconds). Only the payload segment
// is meaningful for tokenTTL. The header and signature are placeholders.
func makeJWT(t *testing.T, exp int64) string {
	t.Helper()
	payload, err := json.Marshal(map[string]int64{"exp": exp})
	require.NoError(t, err)
	seg := base64.RawURLEncoding.EncodeToString(payload)
	return "aGVhZGVy." + seg + ".c2ln"
}

func TestGCPProviderGetToken(t *testing.T) {
	exp := time.Now().Add(45 * time.Minute).Unix()
	jwt := makeJWT(t, exp)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(metadataFlavorHeader) != metadataFlavorValue ||
			r.URL.Query().Get("audience") != "sts.amazonaws.com" ||
			r.URL.Query().Get("format") != "standard" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(jwt))
	}))
	defer server.Close()

	p := &gcpProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		host:     server.URL,
		audience: defaultAudience,
	}

	token, ttl, err := p.GetToken(t.Context())
	require.NoError(t, err)
	require.Equal(t, jwt, token)
	// TTL is derived from the JWT exp claim, so it is close to 45m (allow slack for the elapsed test time).
	require.Greater(t, ttl, 44*time.Minute)
	require.LessOrEqual(t, ttl, 45*time.Minute)
}

func TestGCPProviderGetTokenUsesConfiguredAudience(t *testing.T) {
	var gotAudience string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAudience = r.URL.Query().Get("audience")
		_, _ = w.Write([]byte(makeJWT(t, time.Now().Add(time.Hour).Unix())))
	}))
	defer server.Close()

	p := &gcpProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		host:     server.URL,
		audience: "https://custom.audience/",
	}
	_, _, err := p.GetToken(t.Context())
	require.NoError(t, err)
	require.Equal(t, "https://custom.audience/", gotAudience)
}

func TestGCPProviderGetTokenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	p := &gcpProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		host:     server.URL,
		audience: defaultAudience,
	}

	_, _, err := p.GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
}

func TestGCPProviderGetTokenEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("   \n"))
	}))
	defer server.Close()

	p := &gcpProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		host:     server.URL,
		audience: defaultAudience,
	}

	_, _, err := p.GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty identity token")
}

// TestGCPProviderIsAvailable verifies the probe hits the instance/id leaf with the Metadata-Flavor request
// header and requires the same header echoed back on the response.
func TestGCPProviderIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, instanceIDPath) ||
			r.Header.Get(metadataFlavorHeader) != metadataFlavorValue {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set(metadataFlavorHeader, metadataFlavorValue)
		_, _ = w.Write([]byte("1234567890"))
	}))
	defer server.Close()

	p := &gcpProvider{
		client: &http.Client{Timeout: 5 * time.Second},
		host:   server.URL,
	}
	require.True(t, p.IsAvailable(t.Context()))
}

// TestGCPProviderIsAvailableMissingHeader verifies a 200 without the Metadata-Flavor response header (e.g.
// some other server answering the address) is not treated as GCE.
func TestGCPProviderIsAvailableMissingHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("1234567890"))
	}))
	defer server.Close()

	p := &gcpProvider{
		client: &http.Client{Timeout: 5 * time.Second},
		host:   server.URL,
	}
	require.False(t, p.IsAvailable(t.Context()))
}

// TestGCPProviderMetadataUnreachable points the provider at a closed server so the HTTP request fails,
// exercising the client.Do error paths: IsAvailable reports false and GetToken returns an error.
func TestGCPProviderMetadataUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := server.URL
	server.Close() // nothing listens on host now, so requests are refused

	p := &gcpProvider{
		client:   &http.Client{Timeout: time.Second},
		host:     host,
		audience: defaultAudience,
	}
	require.False(t, p.IsAvailable(t.Context()))
	_, _, err := p.GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata request failed")
}

func TestGCPProviderIsAvailableNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	p := &gcpProvider{
		client: &http.Client{Timeout: 5 * time.Second},
		host:   server.URL,
	}
	require.False(t, p.IsAvailable(t.Context()))
}

func TestNewGCPProviderDefault(t *testing.T) {
	p := New("").(*gcpProvider)
	require.Equal(t, "gcp", p.Name())
	require.Equal(t, defaultAudience, p.audience)
	require.Equal(t, defaultMetadataHost, p.host)
}

func TestNewGCPProviderWithAudience(t *testing.T) {
	p := New("https://custom.audience/").(*gcpProvider)
	require.Equal(t, "https://custom.audience/", p.audience)
}

func TestTokenTTL(t *testing.T) {
	// Valid JWT with an exp ~30m out.
	ttl := tokenTTL(makeJWT(t, time.Now().Add(30*time.Minute).Unix()))
	require.Greater(t, ttl, 29*time.Minute)
	require.LessOrEqual(t, ttl, 30*time.Minute)

	// Malformed tokens and already-expired tokens fall back to the default TTL.
	fallback := defaultTokenExpiry
	require.Equal(t, fallback, tokenTTL("not-a-jwt"))
	require.Equal(t, fallback, tokenTTL("a.b.c"))
	require.Equal(t, fallback, tokenTTL(makeJWT(t, time.Now().Add(-time.Minute).Unix())))
	// A valid-base64 payload that is not JSON, and a token with no usable exp, also fall back.
	require.Equal(t, fallback, tokenTTL("aGVhZGVy."+base64.RawURLEncoding.EncodeToString([]byte("not json"))+".c2ln"))
	require.Equal(t, fallback, tokenTTL(makeJWT(t, 0)))
}
