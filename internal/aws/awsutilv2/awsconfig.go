// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2 // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutilv2"

// AWSSessionSettings defines the common session configs for the v2 AWS credential chain.
type AWSSessionSettings struct {
	// NumberOfWorkers is the maximum idle connections per host.
	NumberOfWorkers int `mapstructure:"num_workers"`
	// Endpoint overrides the AWS service endpoint.
	Endpoint string `mapstructure:"endpoint,omitempty"`
	// RequestTimeoutSeconds is the per-request HTTP timeout in seconds.
	RequestTimeoutSeconds int `mapstructure:"request_timeout_seconds"`
	// MaxRetries is the number of retries beyond the initial attempt.
	MaxRetries int `mapstructure:"max_retries"`
	// NoVerifySSL disables TLS certificate verification.
	NoVerifySSL bool `mapstructure:"no_verify_ssl,omitempty"`
	// ProxyAddress is the HTTP proxy address. When empty, the SDK default (http.ProxyFromEnvironment) honors
	// HTTPS_PROXY/HTTP_PROXY/NO_PROXY at request time.
	ProxyAddress string `mapstructure:"proxy_address,omitempty"`
	// Region is the AWS region for credential resolution and STS calls.
	Region string `mapstructure:"region,omitempty"`
	// LocalMode skips EC2 IMDS region resolution.
	LocalMode bool `mapstructure:"local_mode,omitempty"`
	// ResourceARN is the Amazon Resource Name (ARN) of the AWS resource running the collector.
	ResourceARN string `mapstructure:"resource_arn,omitempty"`
	// RoleARN is the IAM role to assume after resolving root credentials.
	RoleARN string `mapstructure:"role_arn,omitempty"`
	// Profile changes the default profile for the shared credentials file.
	Profile string `mapstructure:"profile,omitempty"`
	// SharedCredentialsFile changes the default shared credentials file location.
	SharedCredentialsFile []string `mapstructure:"shared_credentials_file,omitempty"`
	// CertificateFilePath adds a custom certificates file.
	CertificateFilePath string `mapstructure:"certificate_file_path,omitempty"`
	// IMDSRetries is the number of retries beyond the initial attempt for IMDS region resolution.
	IMDSRetries int `mapstructure:"imds_retries"`
	// ExternalID is used to verify third party role assumption.
	ExternalID string `mapstructure:"external_id,omitempty"`
}

// httpClientSettings is the subset of AWSSessionSettings that determines the HTTP transport
// configuration. Callers with identical settings share a single BuildableClient (and its
// connection pool).
type httpClientSettings struct {
	ProxyAddress          string
	CertificateFilePath   string
	NoVerifySSL           bool
	RequestTimeoutSeconds int
	NumberOfWorkers       int
}

// httpClientSettings returns the transport-relevant subset of settings used as
// a cache key for shared HTTP clients.
func (s *AWSSessionSettings) httpClientSettings() httpClientSettings {
	return httpClientSettings{
		ProxyAddress:          s.ProxyAddress,
		CertificateFilePath:   s.CertificateFilePath,
		NoVerifySSL:           s.NoVerifySSL,
		RequestTimeoutSeconds: s.RequestTimeoutSeconds,
		NumberOfWorkers:       s.NumberOfWorkers,
	}
}

// CreateDefaultSessionConfig returns AWSSessionSettings with sensible defaults. Mirrors v1
// awsutil.CreateDefaultSessionConfig.
func CreateDefaultSessionConfig() AWSSessionSettings {
	return AWSSessionSettings{
		NumberOfWorkers:       8,
		RequestTimeoutSeconds: 30,
		MaxRetries:            2,
	}
}
