// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestBuildHTTPClient_Defaults(t *testing.T) {
	client, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{})
	require.NoError(t, err)
	require.NotNil(t, client)

	assert.Equal(t, time.Duration(0), client.GetTimeout(), "zero RequestTimeoutSeconds yields no timeout")

	transport := client.GetTransport()
	require.NotNil(t, transport)
	assert.Equal(t, 0, transport.MaxIdleConnsPerHost, "zero NumberOfWorkers leaves the default")
	require.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	assert.Nil(t, transport.TLSClientConfig.RootCAs)

	// Compare function pointers because httpproxy.FromEnvironment caches env state via sync.Once,
	// making env-var-based assertions flaky across test order.
	require.NotNil(t, transport.Proxy)
	assert.Equal(t,
		reflect.ValueOf(http.ProxyFromEnvironment).Pointer(),
		reflect.ValueOf(transport.Proxy).Pointer(),
		"expected SDK default http.ProxyFromEnvironment when ProxyAddress is empty")
}

func TestBuildHTTPClient_TimeoutAndWorkers(t *testing.T) {
	client, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{
		RequestTimeoutSeconds: 5,
		NumberOfWorkers:       42,
	})
	require.NoError(t, err)

	assert.Equal(t, 5*time.Second, client.GetTimeout())
	assert.Equal(t, 42, client.GetTransport().MaxIdleConnsPerHost)
}

func TestBuildHTTPClient_NoVerifySSL(t *testing.T) {
	client, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{NoVerifySSL: true})
	require.NoError(t, err)
	assert.True(t, client.GetTransport().TLSClientConfig.InsecureSkipVerify)
}

func TestBuildHTTPClient_ExplicitProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	client, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{ProxyAddress: "http://proxy.example.com:3128"})
	require.NoError(t, err)

	got := resolvedProxy(t, client.GetTransport())
	require.NotNil(t, got)
	assert.Equal(t, "http://proxy.example.com:3128", got.String())
}

func TestBuildHTTPClient_InvalidProxyURL(t *testing.T) {
	_, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{ProxyAddress: "://invalid"})
	require.Error(t, err)
}

func TestBuildHTTPClient_CertificateFilePath(t *testing.T) {
	t.Run("ValidPEM", func(t *testing.T) {
		certPath := writeSelfSignedCert(t)

		core, observed := observer.New(zap.WarnLevel)
		client, err := buildHTTPClient(zap.New(core), &AWSSessionSettings{CertificateFilePath: certPath})
		require.NoError(t, err)

		assert.NotNil(t, client.GetTransport().TLSClientConfig.RootCAs)
		assert.Equal(t, 0, observed.Len(), "no warning expected on a valid bundle")
	})

	t.Run("MissingFile", func(t *testing.T) {
		core, observed := observer.New(zap.WarnLevel)
		client, err := buildHTTPClient(zap.New(core), &AWSSessionSettings{CertificateFilePath: "/nonexistent/ca.pem"})
		require.NoError(t, err, "missing CA file is non-fatal")

		assert.Nil(t, client.GetTransport().TLSClientConfig.RootCAs)
		require.Equal(t, 1, observed.Len())
		assert.Equal(t, "Failed to load custom CA bundle", observed.All()[0].Message)
	})

	t.Run("MalformedPEM", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.pem")
		require.NoError(t, os.WriteFile(path, []byte("not a real PEM"), 0o600))

		core, observed := observer.New(zap.WarnLevel)
		client, err := buildHTTPClient(zap.New(core), &AWSSessionSettings{CertificateFilePath: path})
		require.NoError(t, err, "malformed PEM is non-fatal")

		assert.Nil(t, client.GetTransport().TLSClientConfig.RootCAs)
		require.Equal(t, 1, observed.Len())
		assert.Equal(t, "Failed to load custom CA bundle", observed.All()[0].Message)
	})
}

// TestBuildHTTPClient_AWSCABundleCompatibility verifies that buildHTTPClient returns a type the SDK
// can extend with AWS_CA_BUNDLE / config.WithCustomCABundle. The SDK type-asserts to
// *awshttp.BuildableClient and appends to TLSClientConfig.RootCAs.
func TestBuildHTTPClient_AWSCABundleCompatibility(t *testing.T) {
	certPath := writeSelfSignedCert(t)
	t.Setenv("AWS_CA_BUNDLE", certPath)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true") // avoid IMDS attempts on EC2 hosts

	client, err := buildHTTPClient(zap.NewNop(), &AWSSessionSettings{})
	require.NoError(t, err)

	cfg, err := config.LoadDefaultConfig(t.Context(),
		config.WithHTTPClient(client),
		config.WithRegion(testRegion),
	)
	require.NoError(t, err)

	bc, ok := cfg.HTTPClient.(*awshttp.BuildableClient)
	require.True(t, ok, "SDK should preserve *BuildableClient type after CA-bundle merge")
	require.NotNil(t, bc.GetTransport().TLSClientConfig)
	require.NotNil(t, bc.GetTransport().TLSClientConfig.RootCAs,
		"AWS_CA_BUNDLE should have caused the SDK to populate RootCAs")
}

// resolvedProxy invokes the transport's Proxy func against a synthetic request and returns the URL it
// resolves to.
func resolvedProxy(t *testing.T, transport *http.Transport) *url.URL {
	t.Helper()
	require.NotNil(t, transport.Proxy)
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)
	got, err := transport.Proxy(req)
	require.NoError(t, err)
	return got
}

// writeSelfSignedCert produces a minimal self-signed certificate (ECDSA P-256), writes it as a PEM
// file in t.TempDir, and returns the path. ECDSA is used over RSA for speed: generation is
// microseconds rather than ~100ms.
func writeSelfSignedCert(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
	return path
}
