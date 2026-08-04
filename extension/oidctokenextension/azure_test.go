// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAzureProviderGetToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata") != "true" || r.URL.Query().Get("api-version") != azureIMDSAPIVersion {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := azureTokenResponse{
			AccessToken: "test-token",
			ExpiresIn:   "3600",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := &azureProvider{
		client:             &http.Client{Timeout: 5 * time.Second},
		endpoint:           server.URL,
		configuredResource: defaultAzureResource,
	}

	token, ttl, err := provider.GetToken(t.Context())
	require.NoError(t, err)
	require.Equal(t, "test-token", token)
	require.Equal(t, 3600*time.Second, ttl)
}

func TestAzureProviderGetTokenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	provider := &azureProvider{
		client:             &http.Client{Timeout: 5 * time.Second},
		endpoint:           server.URL,
		configuredResource: defaultAzureResource,
	}

	_, _, err := provider.GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

func TestAzureProviderIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The probe must hit the instance-metadata path derived from the
		// configured endpoint, carry the Metadata header, and use the instance
		// API version.
		if r.URL.Path != azureIMDSInstancePath ||
			r.Header.Get("Metadata") != "true" ||
			r.URL.Query().Get("api-version") != azureIMDSAPIVersion {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := &azureProvider{
		client:             &http.Client{Timeout: 5 * time.Second},
		endpoint:           server.URL,
		configuredResource: defaultAzureResource,
	}

	require.True(t, provider.IsAvailable(t.Context()))
}

func TestAzureProviderIsAvailableNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := &azureProvider{
		client:             &http.Client{Timeout: 5 * time.Second},
		endpoint:           server.URL,
		configuredResource: defaultAzureResource,
	}

	require.False(t, provider.IsAvailable(t.Context()))
}

func TestAzureProviderInstanceMetadataURL(t *testing.T) {
	provider := &azureProvider{endpoint: "http://169.254.169.254/metadata/identity/oauth2/token"}
	require.Equal(t,
		"http://169.254.169.254/metadata/instance?api-version="+azureIMDSAPIVersion,
		provider.instanceMetadataURL("", nil))
}

func TestNewAzureProviderDefault(t *testing.T) {
	provider := newAzureProvider("")
	require.Equal(t, "azure", provider.Name())
	// With no explicit audience, the resource is left for auto-detection.
	require.Empty(t, provider.configuredResource)
	require.Empty(t, provider.resolvedResource)
	require.Equal(t, defaultAzureIMDSEndpoint, provider.endpoint)
}

func TestNewAzureProviderWithResource(t *testing.T) {
	provider := newAzureProvider("https://custom.resource/")
	require.Equal(t, "https://custom.resource/", provider.configuredResource)
}

func TestArmResourceForEnvironment(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"AzurePublicCloud", armResourcePublic},
		{"azurepubliccloud", armResourcePublic},
		{"AZUREPUBLICCLOUD", armResourcePublic},
		{"AzureChinaCloud", armResourceChina},
		{"azurechinacloud", armResourceChina},
		{" AzureChinaCloud ", armResourceChina},
		{"AzureUSGovernmentCloud", armResourceUSGov},
		{"AzureUSGovernment", armResourceUSGov},
		{"", armResourcePublic},
		{"SomethingUnknown", armResourcePublic},
	}
	for _, c := range cases {
		require.Equalf(t, c.want, armResourceForEnvironment(c.env), "env=%q", c.env)
	}
}

// TestResolveResourceConfiguredWins verifies an explicit audience is used
// verbatim and IMDS is never probed for the environment.
func TestResolveResourceConfiguredWins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("azEnvironment must not be probed when audience is configured")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := &azureProvider{
		client:             &http.Client{Timeout: 5 * time.Second},
		endpoint:           server.URL,
		configuredResource: armResourceChina,
	}
	require.Equal(t, armResourceChina, p.resolveResource(t.Context()))
}

// TestResolveResourceDetectsChina verifies auto-detection picks the China ARM
// resource from compute.azEnvironment and caches it.
func TestResolveResourceDetectsChina(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Metadata") != "true" ||
			!strings.HasSuffix(r.URL.Path, "/compute/azEnvironment") ||
			r.URL.Query().Get("format") != "text" ||
			r.URL.Query().Get("api-version") != azureIMDSAPIVersion {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("AzureChinaCloud"))
	}))
	defer server.Close()

	p := &azureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
	}
	require.Equal(t, armResourceChina, p.resolveResource(t.Context()))
	// Second call is served from cache, not a second IMDS probe.
	require.Equal(t, armResourceChina, p.resolveResource(t.Context()))
	require.Equal(t, 1, calls)
}

// TestResolveResourceDetectFailureFallsBack verifies a probe failure yields the
// public fallback and is NOT cached, so a later call can retry.
func TestResolveResourceDetectFailureFallsBack(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := &azureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
	}
	require.Equal(t, armResourcePublic, p.resolveResource(t.Context()))
	require.Empty(t, p.resolvedResource, "failed detection must not be cached")
	require.Equal(t, armResourcePublic, p.resolveResource(t.Context()))
	require.Equal(t, 2, calls, "failed detection should be retried on the next call")
}

// TestGetTokenUsesDetectedResource verifies the end-to-end path: the token
// request carries the auto-detected China ARM resource as the audience. Both
// the instance-metadata probe and the token request are served by one handler
// keyed on path, since they share the endpoint base.
func TestGetTokenUsesDetectedResource(t *testing.T) {
	var gotResource string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/compute/azEnvironment") {
			_, _ = w.Write([]byte("AzureChinaCloud"))
			return
		}
		gotResource = r.URL.Query().Get("resource")
		_ = json.NewEncoder(w).Encode(azureTokenResponse{AccessToken: "t", ExpiresIn: "3600"})
	}))
	defer server.Close()

	p := &azureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
	}
	_, _, err := p.GetToken(t.Context())
	require.NoError(t, err)
	require.Equal(t, armResourceChina, gotResource)
}
