// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

// buildHTTPClient builds an HTTP client from settings for use via config.WithHTTPClient. Returns
// *awshttp.BuildableClient so the SDK can still apply AWS_CA_BUNDLE / config.WithCustomCABundle
// (which type-asserts the registered client).
func buildHTTPClient(logger *zap.Logger, settings *AWSSessionSettings) (*awshttp.BuildableClient, error) {
	if settings.ProxyAddress != "" {
		logger.Debug("Using proxy address", zap.String("proxyAddr", settings.ProxyAddress))
	}

	rootCAs, err := loadCertPool(settings.CertificateFilePath)
	if settings.CertificateFilePath != "" && err != nil {
		logger.Warn("Failed to load custom CA bundle", zap.Error(err))
	}

	proxy, err := getProxyFunc(settings.ProxyAddress)
	if err != nil {
		return nil, err
	}

	client := awshttp.NewBuildableClient().
		WithTimeout(time.Duration(settings.RequestTimeoutSeconds) * time.Second).
		WithTransportOptions(func(t *http.Transport) {
			t.MaxIdleConnsPerHost = settings.NumberOfWorkers
			t.TLSClientConfig = &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: settings.NoVerifySSL,
				RootCAs:            rootCAs,
			}
			t.Proxy = proxy
			// Best-effort HTTP/2. The transport falls back to HTTP/1.1 if configuration fails.
			if herr := http2.ConfigureTransport(t); herr != nil {
				logger.Debug("HTTP/2 configuration failed, falling back to HTTP/1.1", zap.Error(herr))
			}
		})
	return client, nil
}

// getProxyFunc returns a proxy function for use in http.Transport.
// When an explicit proxy address is configured, it returns http.ProxyURL.
// Otherwise, it returns http.ProxyFromEnvironment which respects NO_PROXY.
func getProxyFunc(proxyAddress string) (func(*http.Request) (*url.URL, error), error) {
	if proxyAddress == "" {
		return http.ProxyFromEnvironment, nil
	}
	proxyURL, err := url.Parse(proxyAddress)
	if err != nil {
		return nil, err
	}
	return http.ProxyURL(proxyURL), nil
}

// loadCertPool reads the named PEM file and returns it as an x509 cert pool. An empty path returns
// a nil pool. http.Transport then falls back to the system trust roots.
func loadCertPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	bundle, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(bundle) {
		return nil, fmt.Errorf("no valid PEM certificates found in %s", path)
	}
	return pool, nil
}
