// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
		resource: defaultAzureResource,
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
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
		resource: defaultAzureResource,
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
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
		resource: defaultAzureResource,
	}

	require.True(t, provider.IsAvailable(t.Context()))
}

func TestAzureProviderIsAvailableNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	provider := &azureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
		resource: defaultAzureResource,
	}

	require.False(t, provider.IsAvailable(t.Context()))
}

func TestAzureProviderInstanceMetadataURL(t *testing.T) {
	provider := &azureProvider{endpoint: "http://169.254.169.254/metadata/identity/oauth2/token"}
	require.Equal(t,
		"http://169.254.169.254/metadata/instance?api-version="+azureIMDSAPIVersion,
		provider.instanceMetadataURL())
}

func TestNewAzureProviderDefault(t *testing.T) {
	provider := newAzureProvider("")
	require.Equal(t, "azure", provider.Name())
	require.Equal(t, defaultAzureResource, provider.resource)
	require.Equal(t, defaultAzureIMDSEndpoint, provider.endpoint)
}

func TestNewAzureProviderWithResource(t *testing.T) {
	provider := newAzureProvider("https://custom.resource/")
	require.Equal(t, "https://custom.resource/", provider.resource)
}
