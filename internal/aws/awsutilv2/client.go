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
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

var (
	httpClientsMu sync.Mutex
	httpClients   = map[httpClientSettings]aws.HTTPClient{}
)

// getHTTPClient returns a shared HTTP client for the given settings. Sharing the client enables connection pooling
// and reuse across all AWS API calls, which reduces memory and file descriptor usage. Callers with identical
// transport-relevant settings share a single client.
func getHTTPClient(logger *zap.Logger, settings *AWSSessionSettings) (aws.HTTPClient, error) {
	key := settings.httpClientSettings()
	httpClientsMu.Lock()
	defer httpClientsMu.Unlock()
	if c, ok := httpClients[key]; ok {
		return c, nil
	}
	c, err := buildHTTPClient(logger, key)
	if err != nil {
		return nil, err
	}
	httpClients[key] = c
	return c, nil
}

// buildHTTPClient builds an HTTP client for AWS SDK operations. Returns a BuildableClient because the SDK will only
// append custom CA bundles if the client is of that type. https://github.com/aws/aws-sdk-go-v2/blob/v1.41.12/config/resolve.go#L57
func buildHTTPClient(logger *zap.Logger, settings httpClientSettings) (*awshttp.BuildableClient, error) {
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
