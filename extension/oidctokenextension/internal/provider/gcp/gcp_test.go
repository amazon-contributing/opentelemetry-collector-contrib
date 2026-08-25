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

// pointMetadataAt redirects the compute/metadata client at the test server via GCE_METADATA_HOST, so
// GetToken hits it instead of the real metadata server.
func pointMetadataAt(t *testing.T, server *httptest.Server) {
	t.Helper()
	t.Setenv("GCE_METADATA_HOST", strings.TrimPrefix(server.URL, "http://"))
}

func TestGCPProviderGetToken(t *testing.T) {
	jwt := makeJWT(t, time.Now().Add(45*time.Minute).Unix())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" ||
			r.URL.Query().Get("audience") != "sts.amazonaws.com" ||
			r.URL.Query().Get("format") != "standard" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(jwt))
	}))
	defer server.Close()
	pointMetadataAt(t, server)

	token, ttl, err := New(&http.Client{}, defaultAudience).GetToken(t.Context())
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
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte(makeJWT(t, time.Now().Add(time.Hour).Unix())))
	}))
	defer server.Close()
	pointMetadataAt(t, server)

	_, _, err := New(&http.Client{}, "https://custom.audience/").GetToken(t.Context())
	require.NoError(t, err)
	require.Equal(t, "https://custom.audience/", gotAudience)
}

func TestGCPProviderGetTokenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	pointMetadataAt(t, server)

	_, _, err := New(&http.Client{}, defaultAudience).GetToken(t.Context())
	require.Error(t, err)
}

func TestGCPProviderGetTokenEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Metadata-Flavor", "Google")
		_, _ = w.Write([]byte("   \n"))
	}))
	defer server.Close()
	pointMetadataAt(t, server)

	_, _, err := New(&http.Client{}, defaultAudience).GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty identity token")
}

func TestNewGCPProviderDefault(t *testing.T) {
	p := New(&http.Client{}, "").(*gcpProvider)
	require.Equal(t, "gcp", p.Name())
	require.Equal(t, defaultAudience, p.audience)
}

func TestNewGCPProviderWithAudience(t *testing.T) {
	p := New(&http.Client{}, "https://custom.audience/").(*gcpProvider)
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
