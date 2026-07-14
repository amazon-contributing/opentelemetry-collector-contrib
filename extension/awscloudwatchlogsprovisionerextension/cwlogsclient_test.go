// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
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

func TestDefaultClient_CreateLogGroup_SwallowsOperationAborted(t *testing.T) {
	client := newStubbedCWLogsClient(func(*http.Request) (*http.Response, error) {
		return awsJSONError("OperationAbortedException",
			"Multiple concurrent requests to update the same resource were in conflict."), nil
	})

	assert.NoError(t, client.CreateLogGroup(t.Context(), "/test/group", ""))
}

func TestDefaultClient_CreateLogGroup_SwallowsAlreadyExists(t *testing.T) {
	client := newStubbedCWLogsClient(func(*http.Request) (*http.Response, error) {
		return awsJSONError("ResourceAlreadyExistsException", "The specified log group already exists"), nil
	})

	assert.NoError(t, client.CreateLogGroup(t.Context(), "/test/group", ""))
}

func TestDefaultClient_CreateLogGroup_PropagatesOtherErrors(t *testing.T) {
	client := newStubbedCWLogsClient(func(*http.Request) (*http.Response, error) {
		return awsJSONError("AccessDeniedException", "not authorized"), nil
	})

	assert.Error(t, client.CreateLogGroup(t.Context(), "/test/group", ""))
}

func TestDefaultClient_DescribeRetention_FallbackMemoized(t *testing.T) {
	var identifierCalls, prefixCalls int
	client := newStubbedCWLogsClient(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		if strings.Contains(string(body), "logGroupIdentifiers") {
			identifierCalls++
			return awsJSONError("InvalidParameterException",
				"Input filter on Log group identifiers is not supported."), nil
		}
		prefixCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/x-amz-json-1.1"}},
			Body: io.NopCloser(strings.NewReader(
				`{"logGroups":[{"logGroupName":"/test/group","retentionInDays":90}]}`)),
		}, nil
	})

	// First call: identifiers rejected → falls back to prefix and memoizes.
	got, err := client.DescribeLogGroupsRetention(t.Context(), []string{"/test/group"})
	require.NoError(t, err)
	assert.Equal(t, map[string]int32{"/test/group": 90}, got)
	assert.Equal(t, 1, identifierCalls)
	assert.Equal(t, 1, prefixCalls)

	// Second call: goes straight to prefix, no identifiers attempt.
	_, err = client.DescribeLogGroupsRetention(t.Context(), []string{"/test/group"})
	require.NoError(t, err)
	assert.Equal(t, 1, identifierCalls)
	assert.Equal(t, 2, prefixCalls)
}

func TestIsIdentifiersNotSupported(t *testing.T) {
	notSupported := func(msg string) error {
		return &types.InvalidParameterException{Message: aws.String(msg)}
	}
	assert.True(t, isIdentifiersNotSupported(notSupported("Input filter on Log group identifiers is not supported.")))
	// Loose match survives rewording.
	assert.True(t, isIdentifiersNotSupported(notSupported("Log group Identifiers filter is unsupported")))
	assert.False(t, isIdentifiersNotSupported(notSupported("Invalid limit value")))
	assert.False(t, isIdentifiersNotSupported(errors.New("identifiers")))
	assert.False(t, isIdentifiersNotSupported(nil))
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
