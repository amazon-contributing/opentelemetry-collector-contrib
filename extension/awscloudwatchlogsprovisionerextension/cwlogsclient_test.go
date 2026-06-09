// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"
)

// TestNewDefaultCWLogsClient_CABundle verifies the CA bundle flows into the SDK CW Logs client's
// HTTP transport from either CertificateFilePath or AWS_CA_BUNDLE.
func TestNewDefaultCWLogsClient_CABundle(t *testing.T) {
	t.Run("FromCertificateFilePath", func(t *testing.T) {
		certPath := writeSelfSignedCertForTest(t)

		client, err := newDefaultCWLogsClient(t.Context(), zap.NewNop(), &awsutilv2.AWSSessionSettings{
			Region:              "us-east-1",
			LocalMode:           true,
			CertificateFilePath: certPath,
		})
		require.NoError(t, err)
		assertHTTPClientHasRootCAs(t, client)
	})

	t.Run("FromAWSCABundleEnv", func(t *testing.T) {
		certPath := writeSelfSignedCertForTest(t)
		t.Setenv("AWS_CA_BUNDLE", certPath)

		client, err := newDefaultCWLogsClient(t.Context(), zap.NewNop(), &awsutilv2.AWSSessionSettings{
			Region:    "us-east-1",
			LocalMode: true,
		})
		require.NoError(t, err)
		assertHTTPClientHasRootCAs(t, client)
	})
}

// assertHTTPClientHasRootCAs verifies the SDK CW Logs client's HTTP transport has a custom CA pool.
func assertHTTPClientHasRootCAs(t *testing.T, client cwLogsClient) {
	t.Helper()
	d, ok := client.(*defaultCWLogsClient)
	require.True(t, ok)

	httpClient, ok := d.svc.Options().HTTPClient.(*awshttp.BuildableClient)
	require.True(t, ok, "expected SDK HTTP client to be *awshttp.BuildableClient")

	transport := httpClient.GetTransport()
	require.NotNil(t, transport.TLSClientConfig)
	assert.NotNil(t, transport.TLSClientConfig.RootCAs)
}

// writeSelfSignedCertForTest writes a self-signed cert PEM to a temp file and returns its path.
func writeSelfSignedCertForTest(t *testing.T) string {
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
