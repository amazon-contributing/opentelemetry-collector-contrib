// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension

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
		if r.Header.Get("Metadata") != "true" || r.URL.Query().Get("api-version") != "2018-02-01" {
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

func TestAzureProviderName(t *testing.T) {
	provider := newAzureProvider("")
	require.Equal(t, "azure", provider.Name())
}

func TestNewazureProviderWithResource(t *testing.T) {
	provider := newAzureProvider("https://custom.resource/")
	require.Equal(t, "https://custom.resource/", provider.resource)
}

func TestNewazureProviderDefaultResource(t *testing.T) {
	provider := newAzureProvider("")
	require.Equal(t, defaultAzureResource, provider.resource)
}
