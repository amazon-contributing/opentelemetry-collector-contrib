// Copyright The OpenTelemetry Authors
// Portions of this file Copyright 2018-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
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

// getHTTPClient returns a shared HTTP client for the given settings. Sharing the
// client enables connection pooling and reuse across all AWS API calls, which
// reduces memory and file descriptor usage. Callers with identical
// transport-relevant settings share a single client.
func getHTTPClient(logger *zap.Logger, settings *AWSSessionSettings) (aws.HTTPClient, error) {
	key := settings.httpClientSettings()
	httpClientsMu.Lock()
	defer httpClientsMu.Unlock()
	if c, ok := httpClients[key]; ok {
		return c, nil
	}
	c, err := newHTTPClient(logger, key)
	if err != nil {
		return nil, err
	}
	httpClients[key] = c
	return c, nil
}

// newHTTPClient returns an aws.HTTPClient backed by an
// *awshttp.BuildableClient. This client is attached to the returned config
// after config.LoadDefaultConfig, so it applies to data-plane service
// clients only — never to IMDS, the credential chain, or STS.
//
// settings.CertificateFilePath, when non-empty, is parsed into an empty x509
// pool (system CAs are intentionally not included; operators who need both
// must combine them in the bundle file).
func newHTTPClient(logger *zap.Logger, settings httpClientSettings) (aws.HTTPClient, error) {
	rootCAs, certPoolErr := loadCertPool(settings.CertificateFilePath)
	if settings.CertificateFilePath != "" && certPoolErr != nil {
		logger.Warn("could not create root ca from",
			zap.String("file", settings.CertificateFilePath), zap.Error(certPoolErr))
	}

	proxyFunc, err := GetProxyFunc(settings.ProxyAddress)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return nil, err
	}

	client := awshttp.NewBuildableClient().
		WithTimeout(time.Duration(settings.RequestTimeoutSeconds) * time.Second).
		WithTransportOptions(func(t *http.Transport) {
			t.MaxIdleConnsPerHost = settings.NumberOfWorkers
			t.TLSClientConfig = &tls.Config{
				RootCAs:            rootCAs,
				InsecureSkipVerify: settings.NoVerifySSL,
			}
			t.Proxy = proxyFunc
			// Best-effort HTTP/2; safe to ignore the error since the
			// transport falls back to HTTP/1.1.
			_ = http2.ConfigureTransport(t)
		})

	return client, nil
}

// GetProxyFunc returns the proxy resolver for an *http.Transport. An
// empty proxyAddress falls through to http.ProxyFromEnvironment, which
// honors HTTP_PROXY, HTTPS_PROXY, and NO_PROXY (upper- and lowercase).
func GetProxyFunc(proxyAddress string) (func(*http.Request) (*url.URL, error), error) {
	if proxyAddress == "" {
		return http.ProxyFromEnvironment, nil
	}
	proxyURL, err := url.Parse(proxyAddress)
	if err != nil {
		return nil, err
	}
	return http.ProxyURL(proxyURL), nil
}

// loadCertPool parses a PEM bundle from bundleFile into an empty x509
// pool. System CAs are not included.
func loadCertPool(bundleFile string) (*x509.CertPool, error) {
	bundleBytes, err := os.ReadFile(bundleFile)
	if err != nil {
		return nil, err
	}
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(bundleBytes) {
		return nil, errors.New("unable to append certs")
	}
	return p, nil
}

// ProxyServerTransport returns an *http.Transport for the X-Ray signing
// proxy. DisableCompression is true so the proxy does not gzip a body
// the upstream client has already SigV4-signed (gzip would invalidate
// the signature).
func ProxyServerTransport(logger *zap.Logger, config *AWSSessionSettings) (*http.Transport, error) {
	proxyFunc, err := GetProxyFunc(config.ProxyAddress)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return nil, err
	}

	return &http.Transport{
		MaxIdleConns:        config.NumberOfWorkers,
		MaxIdleConnsPerHost: config.NumberOfWorkers,
		IdleConnTimeout:     time.Duration(config.RequestTimeoutSeconds) * time.Second,
		Proxy:               proxyFunc,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: config.NoVerifySSL,
		},
		DisableCompression: true,
	}, nil
}
