// Copyright The OpenTelemetry Authors
// Portions of this file Copyright 2018-2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package awsutil // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	override "github.com/amazon-contributing/opentelemetry-collector-contrib/override/aws"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/defaults"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
)

type ConnAttr interface {
	newAWSSession(logger *zap.Logger, roleArn string, region string, profile string, sharedCredentialsFile []string) (*session.Session, error)
	getEC2Region(s *session.Session) (string, error)
}

// Conn implements connAttr interface.
type Conn struct{}

func (c *Conn) getEC2Region(s *session.Session) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), override.TimePerCall)
	defer cancel()
	region, err := ec2metadata.New(s, &aws.Config{
		Retryer:                   override.IMDSRetryer,
		EC2MetadataEnableFallback: aws.Bool(false),
	}).RegionWithContext(ctx)
	if err == nil {
		return region, err
	}
	ctxFallbackEnable, cancelFallbackEnable := context.WithTimeout(context.Background(), override.TimePerCall)
	defer cancelFallbackEnable()
	return ec2metadata.New(s, &aws.Config{}).RegionWithContext(ctxFallbackEnable)
}

// AWS STS endpoint constants
const (
	STSEndpointPrefix         = "https://sts."
	STSEndpointSuffix         = ".amazonaws.com"
	STSAwsCnPartitionIDSuffix = ".amazonaws.com.cn" // AWS China partition.
)

// newHTTPClient returns new HTTP client instance with provided configuration.
func newHTTPClient(logger *zap.Logger, maxIdle int, requestTimeout int, noVerify bool,
	proxyAddress string, certificateFilePath string) (*http.Client, error) {
	logger.Debug("Using proxy address: ",
		zap.String("proxyAddr", proxyAddress),
	)
	certificateList, err := loadCertificateAndKeyFromFile(certificateFilePath, logger)
	var tlsConfig *tls.Config
	if err != nil {
		logger.Debug("could not get cert from file", zap.Error(err))
		tlsConfig = &tls.Config{
			InsecureSkipVerify: noVerify,
		}
	} else {
		tlsConfig = &tls.Config{
			Certificates:       certificateList,
			InsecureSkipVerify: noVerify,
		}
	}

	finalProxyAddress := getProxyAddress(proxyAddress)
	proxyURL, err := getProxyURL(finalProxyAddress)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return nil, err
	}
	transport := &http.Transport{
		MaxIdleConnsPerHost: maxIdle,
		TLSClientConfig:     tlsConfig,
		Proxy:               http.ProxyURL(proxyURL),
	}

	// is not enabled by default as we configure TLSClientConfig for supporting SSL to data plane.
	// http2.ConfigureTransport will setup transport layer to use HTTP2
	if err = http2.ConfigureTransport(transport); err != nil {
		logger.Error("unable to configure http2 transport", zap.Error(err))
		return nil, err
	}

	http := &http.Client{
		Transport: transport,
		Timeout:   time.Second * time.Duration(requestTimeout),
	}
	return http, err
}

func loadCertificateAndKeyFromFile(path string, logger *zap.Logger) ([]tls.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cert := make([]tls.Certificate, 0)
	for {
		block, rest := pem.Decode(raw)
		if block == nil {
			break
		}

		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}

		cert = append(cert, tls.Certificate{
			Certificate: [][]byte{block.Bytes},
			Leaf:        certificate,
		})
		logger.Debug("cert added", zap.Any("dns", certificate.DNSNames))
		raw = rest
	}

	if len(cert) == 0 {
		return nil, fmt.Errorf("no certificate found in \"%s\"", path)
	}

	return cert, nil
}

func getProxyAddress(proxyAddress string) string {
	var finalProxyAddress string
	switch {
	case proxyAddress != "":
		finalProxyAddress = proxyAddress

	case proxyAddress == "" && os.Getenv("HTTPS_PROXY") != "":
		finalProxyAddress = os.Getenv("HTTPS_PROXY")
	default:
		finalProxyAddress = ""
	}
	return finalProxyAddress
}

func getProxyURL(finalProxyAddress string) (*url.URL, error) {
	var proxyURL *url.URL
	var err error
	if finalProxyAddress != "" {
		proxyURL, err = url.Parse(finalProxyAddress)
	} else {
		proxyURL = nil
		err = nil
	}
	return proxyURL, err
}

// GetAWSConfigSession returns AWS config and session instances.
func GetAWSConfigSession(logger *zap.Logger, cn ConnAttr, cfg *AWSSessionSettings) (*aws.Config, *session.Session, error) {
	var s *session.Session
	var err error
	var awsRegion string
	http, err := newHTTPClient(logger, cfg.NumberOfWorkers, cfg.RequestTimeoutSeconds, cfg.NoVerifySSL, cfg.ProxyAddress, cfg.CertificateFilePath)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return nil, nil, err
	}
	regionEnv := os.Getenv("AWS_REGION")

	switch {
	case cfg.Region == "" && regionEnv != "":
		awsRegion = regionEnv
		logger.Debug("Fetch region from environment variables", zap.String("region", awsRegion))
	case cfg.Region != "":
		awsRegion = cfg.Region
		logger.Debug("Fetch region from commandline/config file", zap.String("region", awsRegion))
	case !cfg.NoVerifySSL:
		var es *session.Session
		es, err = GetDefaultSession(logger, session.Options{})
		if err != nil {
			logger.Error("Unable to retrieve default session", zap.Error(err))
		} else {
			awsRegion, err = cn.getEC2Region(es)
			if err != nil {
				logger.Error("Unable to retrieve the region from the EC2 instance", zap.Error(err))
			} else {
				logger.Debug("Fetch region from ec2 metadata", zap.String("region", awsRegion))
			}
		}

	}

	if awsRegion == "" {
		msg := "Cannot fetch region variable from config file, environment variables and ec2 metadata."
		logger.Error(msg)
		return nil, nil, awserr.New("NoAwsRegion", msg, nil)
	}
	s, err = cn.newAWSSession(logger, cfg.RoleARN, awsRegion, cfg.Profile, cfg.SharedCredentialsFile)
	if err != nil {
		return nil, nil, err
	}

	config := &aws.Config{
		Region:                        aws.String(awsRegion),
		DisableParamValidation:        aws.Bool(true),
		MaxRetries:                    aws.Int(cfg.MaxRetries),
		Endpoint:                      aws.String(cfg.Endpoint),
		HTTPClient:                    http,
		CredentialsChainVerboseErrors: aws.Bool(true),
	}
	// do not overwrite for sts assume role
	if cfg.RoleARN == "" && len(override.GetCredentialsChainOverride().GetCredentialsChain()) > 0 {
		config.Credentials = credentials.NewCredentials(&credentials.ChainProvider{
			Providers: customCredentialProvider(cfg, config),
		})
	}
	return config, s, nil
}

func customCredentialProvider(cfg *AWSSessionSettings, config *aws.Config) []credentials.Provider {
	defaultCredProviders := defaults.CredProviders(config, defaults.Handlers())
	overrideCredProviders := override.GetCredentialsChainOverride().GetCredentialsChain()
	credProviders := make([]credentials.Provider, 0)
	// if is for differently configured shared creds file location
	// else if is for diff profile but no change in creds file ex run in containers
	if cfg.SharedCredentialsFile != nil && len(cfg.SharedCredentialsFile) > 0 {
		for _, file := range cfg.SharedCredentialsFile {
			credProviders = append(credProviders, &credentials.SharedCredentialsProvider{Filename: file, Profile: cfg.Profile})
		}
	} else if cfg.Profile != "" {
		credProviders = append(credProviders, &credentials.SharedCredentialsProvider{Filename: "", Profile: cfg.Profile})
	}
	credProviders = append(credProviders, defaultCredProviders...)
	for _, provider := range overrideCredProviders {
		for _, file := range cfg.SharedCredentialsFile {
			credProviders = append(credProviders, provider(file))
		}
	}
	return credProviders
}

// ProxyServerTransport configures HTTP transport for TCP Proxy Server.
func ProxyServerTransport(logger *zap.Logger, config *AWSSessionSettings) (*http.Transport, error) {
	tls := &tls.Config{
		InsecureSkipVerify: config.NoVerifySSL,
	}

	proxyAddr := getProxyAddress(config.ProxyAddress)
	proxyURL, err := getProxyURL(proxyAddr)
	if err != nil {
		logger.Error("unable to obtain proxy URL", zap.Error(err))
		return nil, err
	}

	// Connection timeout in seconds
	idleConnTimeout := time.Duration(config.RequestTimeoutSeconds) * time.Second

	transport := &http.Transport{
		MaxIdleConns:        config.NumberOfWorkers,
		MaxIdleConnsPerHost: config.NumberOfWorkers,
		IdleConnTimeout:     idleConnTimeout,
		Proxy:               http.ProxyURL(proxyURL),
		TLSClientConfig:     tls,

		// If not disabled the transport will add a gzip encoding header
		// to requests with no `accept-encoding` header value. The header
		// is added after we sign the request which invalidates the
		// signature.
		DisableCompression: true,
	}

	return transport, nil
}

func (c *Conn) newAWSSession(logger *zap.Logger, roleArn string, region string, profile string, sharedCredentialsFile []string) (*session.Session, error) {
	var s *session.Session
	var err error
	if roleArn == "" {
		// if an empty or nil list of sharedCredentialsFile is passed use the sdk default
		if sharedCredentialsFile == nil || len(sharedCredentialsFile) < 1 {
			sharedCredentialsFile = nil
		}
		options := session.Options{
			Profile:           profile,
			SharedConfigFiles: sharedCredentialsFile,
		}
		s, err = GetDefaultSession(logger, options)
		if err != nil {
			return s, err
		}
	} else {
		stsCreds, _ := getSTSCreds(logger, region, roleArn)

		s, err = session.NewSession(&aws.Config{
			Credentials: stsCreds,
		})

		if err != nil {
			logger.Error("Error in creating session object : ", zap.Error(err))
			return s, err
		}
	}
	return s, nil
}

// getSTSCreds gets STS credentials from regional endpoint. ErrCodeRegionDisabledException is received if the
// STS regional endpoint is disabled. In this case STS credentials are fetched from STS primary regional endpoint
// in the respective AWS partition.
func getSTSCreds(logger *zap.Logger, region string, roleArn string) (*credentials.Credentials, error) {
	t, err := GetDefaultSession(logger, session.Options{})
	if err != nil {
		return nil, err
	}

	stsCred := getSTSCredsFromRegionEndpoint(logger, t, region, roleArn)
	// Make explicit call to fetch credentials.
	_, err = stsCred.Get()
	if err != nil {
		var awsErr awserr.Error
		if errors.As(err, &awsErr) {
			err = nil

			if awsErr.Code() == sts.ErrCodeRegionDisabledException {
				logger.Error("Region ", zap.String("region", region), zap.Error(awsErr))
				stsCred = getSTSCredsFromPrimaryRegionEndpoint(logger, t, roleArn, region)
			}
		}
	}
	return stsCred, err
}

// getSTSCredsFromRegionEndpoint fetches STS credentials for provided roleARN from regional endpoint.
// AWS STS recommends that you provide both the Region and endpoint when you make calls to a Regional endpoint.
// Reference: https://docs.aws.amazon.com/IAM/latest/UserGuide/id_credentials_temp_enable-regions.html#id_credentials_temp_enable-regions_writing_code
func getSTSCredsFromRegionEndpoint(logger *zap.Logger, sess *session.Session, region string,
	roleArn string) *credentials.Credentials {
	regionalEndpoint := getSTSRegionalEndpoint(region)
	// if regionalEndpoint is "", the STS endpoint is Global endpoint for classic regions except ap-east-1 - (HKG)
	// for other opt-in regions, region value will create STS regional endpoint.
	// This will be only in the case, if provided region is not present in aws_regions.go
	c := &aws.Config{Region: aws.String(region), Endpoint: &regionalEndpoint}
	st := sts.New(sess, c)
	logger.Info("STS Endpoint ", zap.String("endpoint", st.Endpoint))
	return stscreds.NewCredentialsWithClient(st, roleArn)
}

// getSTSCredsFromPrimaryRegionEndpoint fetches STS credentials for provided roleARN from primary region endpoint in
// the respective partition.
func getSTSCredsFromPrimaryRegionEndpoint(logger *zap.Logger, t *session.Session, roleArn string,
	region string) *credentials.Credentials {
	logger.Info("Credentials for provided RoleARN being fetched from STS primary region endpoint.")
	partitionID := getPartition(region)
	switch partitionID {
	case endpoints.AwsPartitionID:
		return getSTSCredsFromRegionEndpoint(logger, t, endpoints.UsEast1RegionID, roleArn)
	case endpoints.AwsCnPartitionID:
		return getSTSCredsFromRegionEndpoint(logger, t, endpoints.CnNorth1RegionID, roleArn)
	case endpoints.AwsUsGovPartitionID:
		return getSTSCredsFromRegionEndpoint(logger, t, endpoints.UsGovWest1RegionID, roleArn)
	}

	return nil
}

func getSTSRegionalEndpoint(r string) string {
	p := getPartition(r)

	var e string
	if p == endpoints.AwsPartitionID || p == endpoints.AwsUsGovPartitionID {
		e = STSEndpointPrefix + r + STSEndpointSuffix
	} else if p == endpoints.AwsCnPartitionID {
		e = STSEndpointPrefix + r + STSAwsCnPartitionIDSuffix
	}
	return e
}

func GetDefaultSession(logger *zap.Logger, options session.Options) (*session.Session, error) {
	result, serr := session.NewSessionWithOptions(options)
	if serr != nil {
		logger.Error("Error in creating session object ", zap.Error(serr))
		return result, serr
	}
	return result, nil
}

// getPartition return AWS Partition for the provided region.
func getPartition(region string) string {
	p, _ := endpoints.PartitionForRegion(endpoints.DefaultPartitions(), region)
	return p.ID()
}
