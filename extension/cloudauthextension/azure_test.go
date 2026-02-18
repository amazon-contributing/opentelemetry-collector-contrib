// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAzureProviderGetToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "true", r.Header.Get("Metadata"))
		require.Equal(t, "2018-02-01", r.URL.Query().Get("api-version"))

		resp := azureTokenResponse{
			AccessToken: "test-token",
			ExpiresIn:   "3600",
		}
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer server.Close()

	provider := &AzureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
		resource: defaultAzureResource,
	}

	token, ttl, err := provider.GetToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-token", token)
	require.Equal(t, 3600*time.Second, ttl)
}

func TestAzureProviderGetTokenError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, err := w.Write([]byte("unauthorized"))
		require.NoError(t, err)
	}))
	defer server.Close()

	provider := &AzureProvider{
		client:   &http.Client{Timeout: 5 * time.Second},
		endpoint: server.URL,
		resource: defaultAzureResource,
	}

	_, _, err := provider.GetToken(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

func TestAzureProviderName(t *testing.T) {
	provider := newAzureProvider("")
	require.Equal(t, "azure", provider.Name())
}

func TestNewAzureProviderWithResource(t *testing.T) {
	provider := newAzureProvider("https://custom.resource/")
	require.Equal(t, "https://custom.resource/", provider.resource)
}

func TestNewAzureProviderDefaultResource(t *testing.T) {
	provider := newAzureProvider("")
	require.Equal(t, defaultAzureResource, provider.resource)
}
