// Copyright The OpenTelemetry Authors
// Portions of this file Copyright 2018-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

// AWSSessionSettings defines the common session configs for AWS components
type AWSSessionSettings struct {
	// Maximum number of concurrent calls to AWS X-Ray to upload documents.
	NumberOfWorkers int `mapstructure:"num_workers"`
	// X-Ray service endpoint to which the collector sends segment documents.
	Endpoint string `mapstructure:"endpoint,omitempty"`
	// Number of seconds before timing out a request.
	RequestTimeoutSeconds int `mapstructure:"request_timeout_seconds"`
	// Maximum number of retries on top of the initial attempt before
	// abandoning an attempt to post data. Total attempts = MaxRetries + 1.
	MaxRetries int `mapstructure:"max_retries"`
	// Enable or disable TLS certificate verification.
	NoVerifySSL bool `mapstructure:"no_verify_ssl,omitempty"`
	// Upload segments to AWS X-Ray through a proxy.
	ProxyAddress string `mapstructure:"proxy_address,omitempty"`
	// Send segments to AWS X-Ray service in a specific region.
	Region string `mapstructure:"region,omitempty"`
	// Local mode to skip EC2 instance metadata check.
	LocalMode bool `mapstructure:"local_mode,omitempty"`
	// Amazon Resource Name (ARN) of the AWS resource running the collector.
	ResourceARN string `mapstructure:"resource_arn,omitempty"`
	// IAM role to upload segments to a different account.
	RoleARN string `mapstructure:"role_arn,omitempty"`
	// Change the default profile for shared creds file
	Profile string `mapstructure:"profile,omitempty"`
	// Change the default shared creds file location
	SharedCredentialsFile []string `mapstructure:"shared_credentials_file,omitempty"`
	// Add a custom certificates file
	CertificateFilePath string `mapstructure:"certificate_file_path,omitempty"`
	// How many times should we retry imds v2
	IMDSRetries int `mapstructure:"imds_retries"`
	// External ID to verify third party role assumption
	ExternalID string `mapstructure:"external_id,omitempty"`
}

// httpClientSettings is the subset of AWSSessionSettings that determines the HTTP
// transport configuration. Callers with identical settings share a single
// BuildableClient (and its connection pool).
type httpClientSettings struct {
	ProxyAddress          string
	CertificateFilePath   string
	NoVerifySSL           bool
	RequestTimeoutSeconds int
	NumberOfWorkers       int
}

// httpClientSettings returns the transport-relevant subset of settings used as a
// cache key for shared HTTP clients.
func (s *AWSSessionSettings) httpClientSettings() httpClientSettings {
	return httpClientSettings{
		ProxyAddress:          s.ProxyAddress,
		CertificateFilePath:   s.CertificateFilePath,
		NoVerifySSL:           s.NoVerifySSL,
		RequestTimeoutSeconds: s.RequestTimeoutSeconds,
		NumberOfWorkers:       s.NumberOfWorkers,
	}
}

func CreateDefaultSessionConfig() AWSSessionSettings {
	return AWSSessionSettings{
		NumberOfWorkers:       8,
		Endpoint:              "",
		RequestTimeoutSeconds: 30,
		MaxRetries:            2,
		NoVerifySSL:           false,
		ProxyAddress:          "",
		Region:                "",
		LocalMode:             false,
		ResourceARN:           "",
		RoleARN:               "",
	}
}
