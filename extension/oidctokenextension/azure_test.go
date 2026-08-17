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
		configuredResource: armResourcePublic,
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
		configuredResource: armResourcePublic,
	}

	_, _, err := provider.GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// TestAzureProviderIsAvailable verifies the probe hits the azEnvironment leaf
// (with the Metadata header, instance API version, and text format), reports
// availability on 200, and caches the ARM resource derived from the response.
func TestAzureProviderIsAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/compute/azEnvironment") ||
			r.Header.Get("Metadata") != "true" ||
			r.URL.Query().Get("format") != "text" ||
			r.URL.Query().Get("api-version") != azureIMDSAPIVersion {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("AzureChinaCloud"))
	}))
	defer server.Close()

	provider := &azureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
	}

	require.True(t, provider.IsAvailable(t.Context()))
	// The probe caches the resource derived from azEnvironment.
	require.Equal(t, armResourceChina, provider.resource())
}

func TestAzureProviderIsAvailableNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := &azureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
	}

	require.False(t, provider.IsAvailable(t.Context()))
	// Nothing cached on a failed probe; resource() falls back to public ARM.
	require.Equal(t, armResourcePublic, provider.resource())
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
	// With no explicit audience and no successful probe yet, resource() falls
	// back to the public ARM resource.
	require.Empty(t, provider.configuredResource)
	require.Equal(t, armResourcePublic, provider.resource())
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

// TestResourceConfiguredWins verifies an explicit audience is used verbatim and
// takes precedence over any probe-detected value.
func TestResourceConfiguredWins(t *testing.T) {
	p := &azureProvider{configuredResource: armResourceChina}
	// Even if the probe had cached something else, the configured value wins.
	p.resolvedResource.Store(armResourceUSGov)
	require.Equal(t, armResourceChina, p.resource())
}

// TestResourceFallsBackToPublic verifies resource() returns public ARM when
// neither a configured audience nor a probe-detected value is present.
func TestResourceFallsBackToPublic(t *testing.T) {
	p := &azureProvider{}
	require.Equal(t, armResourcePublic, p.resource())
}

// TestGetTokenUsesDetectedResource verifies the end-to-end path: after the
// probe detects Azure China, the token request carries the China ARM resource
// as the audience. The probe (azEnvironment leaf) and token request share the
// endpoint base and are routed by path.
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
	require.True(t, p.IsAvailable(t.Context()))
	_, _, err := p.GetToken(t.Context())
	require.NoError(t, err)
	require.Equal(t, armResourceChina, gotResource)
}
