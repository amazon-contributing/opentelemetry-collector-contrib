// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsutil

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestGetProxyFunc(t *testing.T) {
	t.Run("ExplicitProxyAddressWins", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://env-http-proxy:8080")
		t.Setenv("HTTPS_PROXY", "http://env-https-proxy:8080")
		t.Setenv("NO_PROXY", "")

		fn, err := GetProxyFunc("http://explicit:9999")
		require.NoError(t, err)
		require.NotNil(t, fn)

		req, _ := http.NewRequest(http.MethodGet, "https://anything.example.com/", http.NoBody)
		u, err := fn(req)
		require.NoError(t, err)
		assert.Equal(t, "http://explicit:9999", u.String())
	})

	t.Run("EmptyFallsThroughToEnvironment", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://env-https-proxy:8080")
		t.Setenv("NO_PROXY", "")

		fn, err := GetProxyFunc("")
		require.NoError(t, err)
		require.NotNil(t, fn)

		req, _ := http.NewRequest(http.MethodGet, "https://anything.example.com/", http.NoBody)
		u, err := fn(req)
		require.NoError(t, err)
		require.NotNil(t, u)
		assert.Equal(t, "http://env-https-proxy:8080", u.String())
	})

	t.Run("InvalidProxyAddressReturnsError", func(t *testing.T) {
		fn, err := GetProxyFunc("http://bad-percent-encoding%")
		assert.Error(t, err)
		assert.Nil(t, fn)
	})
}

func TestLoadCertPool(t *testing.T) {
	t.Run("ValidPEM", func(t *testing.T) {
		pool, err := loadCertPool(filepath.Join("testdata", "public_amazon_cert.pem"))
		require.NoError(t, err)
		assert.NotNil(t, pool)
	})

	t.Run("MissingFile", func(t *testing.T) {
		_, err := loadCertPool(filepath.Join(t.TempDir(), "no_such_file.pem"))
		assert.Error(t, err)
	})

	t.Run("EmptyPath", func(t *testing.T) {
		_, err := loadCertPool("")
		assert.Error(t, err)
	})

	t.Run("MalformedPEM", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "junk.pem")
		require.NoError(t, os.WriteFile(f, []byte("this is not a PEM"), 0o600))
		_, err := loadCertPool(f)
		assert.Error(t, err)
	})
}

func TestNewHTTPClient(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		client, err := newHTTPClient(zap.NewNop(), httpClientSettings{NumberOfWorkers: 8, RequestTimeoutSeconds: 30})
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("InvalidProxyPropagatesError", func(t *testing.T) {
		_, err := newHTTPClient(zap.NewNop(), httpClientSettings{NumberOfWorkers: 8, RequestTimeoutSeconds: 30, ProxyAddress: "http://bad-percent%"})
		assert.Error(t, err)
	})

	t.Run("InvalidCertFileLogsAndContinues", func(t *testing.T) {
		// A typo in CertificateFilePath logs a warning and falls back to
		// system trust rather than failing client construction.
		client, err := newHTTPClient(zap.NewNop(), httpClientSettings{NumberOfWorkers: 8, RequestTimeoutSeconds: 30, CertificateFilePath: filepath.Join(t.TempDir(), "missing")})
		require.NoError(t, err)
		require.NotNil(t, client)
	})
}

func TestProxyServerTransport(t *testing.T) {
	cfg := &AWSSessionSettings{
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
		NoVerifySSL:           true,
	}
	tr, err := ProxyServerTransport(zap.NewNop(), cfg)
	require.NoError(t, err)
	require.NotNil(t, tr)
	assert.True(t, tr.DisableCompression)
	assert.Equal(t, 8, tr.MaxIdleConns)
	assert.Equal(t, 8, tr.MaxIdleConnsPerHost)
	require.NotNil(t, tr.TLSClientConfig)
	assert.True(t, tr.TLSClientConfig.InsecureSkipVerify)
}

func TestGetHTTPClient_CachesByConfig(t *testing.T) {
	// Reset the cache for test isolation.
	httpClientsMu.Lock()
	httpClients = map[httpClientSettings]aws.HTTPClient{}
	httpClientsMu.Unlock()
	t.Cleanup(func() {
		httpClientsMu.Lock()
		httpClients = map[httpClientSettings]aws.HTTPClient{}
		httpClientsMu.Unlock()
	})

	settingsA := &AWSSessionSettings{RequestTimeoutSeconds: 30, NumberOfWorkers: 8}
	settingsB := &AWSSessionSettings{RequestTimeoutSeconds: 30, NumberOfWorkers: 8}
	settingsC := &AWSSessionSettings{RequestTimeoutSeconds: 60, NumberOfWorkers: 8}

	clientA, err := getHTTPClient(zap.NewNop(), settingsA)
	require.NoError(t, err)
	clientB, err := getHTTPClient(zap.NewNop(), settingsB)
	require.NoError(t, err)
	clientC, err := getHTTPClient(zap.NewNop(), settingsC)
	require.NoError(t, err)

	assert.Same(t, clientA, clientB, "identical settings should return the same client")
	assert.NotSame(t, clientA, clientC, "different settings should return different clients")
}

func TestGetHTTPClient_Concurrent(t *testing.T) {
	httpClientsMu.Lock()
	httpClients = map[httpClientSettings]aws.HTTPClient{}
	httpClientsMu.Unlock()
	t.Cleanup(func() {
		httpClientsMu.Lock()
		httpClients = map[httpClientSettings]aws.HTTPClient{}
		httpClientsMu.Unlock()
	})

	settings := &AWSSessionSettings{RequestTimeoutSeconds: 30, NumberOfWorkers: 8}
	const count = 50
	clients := make([]aws.HTTPClient, count)
	var wg sync.WaitGroup

	for i := range count {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			c, err := getHTTPClient(zap.NewNop(), settings)
			assert.NoError(t, err)
			clients[index] = c
		}(i)
	}
	wg.Wait()

	first := clients[0]
	assert.NotNil(t, first)
	for i := 1; i < count; i++ {
		assert.Same(t, first, clients[i])
	}
}
