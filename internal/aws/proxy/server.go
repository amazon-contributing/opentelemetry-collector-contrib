// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package proxy provides an http server to act as a signing proxy for SDKs calling AWS X-Ray APIs
package proxy // import "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/proxy"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	v4 "github.com/aws/aws-sdk-go/aws/signer/v4"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/common/sanitize"
)

const (
	connHeader = "Connection"
)

// Server represents HTTP server.
type Server interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

// NewServer returns a local TCP server that proxies requests to AWS
// backend using the given credentials.
func NewServer(cfg *Config, logger *zap.Logger) (Server, error) {
	_, err := net.ResolveTCPAddr("tcp", cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if cfg.ProxyAddress != "" {
		logger.Debug("Using remote proxy", zap.String("address", cfg.ProxyAddress))
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "xray"
	}

	sessionCfg := cfg.toSessionConfig()
	awsCfg, sess, err := awsutil.GetAWSConfigSession(logger, &awsutil.Conn{}, sessionCfg)
	if err != nil {
		return nil, err
	}

	awsEndPoint, err := getServiceEndpoint(awsCfg, cfg.ServiceName)
	if err != nil {
		return nil, err
	}

	// Parse url from endpoint
	awsURL, err := url.Parse(awsEndPoint)
	if err != nil {
		return nil, fmt.Errorf("unable to parse AWS service endpoint: %w", err)
	}

	signer := &v4.Signer{
		Credentials: sess.Config.Credentials,
	}

	// Creates an API route map and create a map for each unique role to an associated AWS signer.
	// Each additional routing rule can define its own role_arn to authenticate with different AWS credentials.
	// We create signers at startup for all unique roles so we can select the appropriate signer at request time.
	apiRouteMap, signerMap, err := buildRoutingMaps(cfg.AdditionalRoutingRules, cfg.RoleARN, signer, cfg.AWSEndpoint, sessionCfg, logger)
	if err != nil {
		return nil, err
	}

	transport, err := awsutil.ProxyServerTransport(logger, sessionCfg)
	if err != nil {
		return nil, err
	}

	// Reverse proxy handler
	handler := &httputil.ReverseProxy{
		Transport: transport,

		// Handler for modifying and forwarding requests
		Director: func(req *http.Request) {
			if req != nil && req.URL != nil {
				logger.Debug("Received request on X-Ray receiver TCP proxy server", zap.String("URL", sanitize.URL(req.URL)))
			}

			// Remove connection header before signing request, otherwise the
			// reverse-proxy will remove the header before forwarding to X-Ray
			// resulting in a signed header being missing from the request.
			req.Header.Del(connHeader)

			apiName := strings.TrimPrefix(req.URL.Path, "/")
			serviceName := cfg.ServiceName
			region := *awsCfg.Region
			endpoint := awsEndPoint

			// Check for custom routing rules
			if serviceConfig := apiRouteMap[apiName]; serviceConfig != nil {
				if serviceConfig.ServiceName != "" {
					serviceName = serviceConfig.ServiceName
				}
				if serviceConfig.Region != "" {
					region = serviceConfig.Region
				}
				if serviceConfig.RoleARN != "" {
					if roleSigner, ok := signerMap[serviceConfig.RoleARN]; ok {
						signer = roleSigner
					}
				}
				if serviceConfig.AWSEndpoint != "" {
					endpoint = serviceConfig.AWSEndpoint
				}
			}

			targetURL, err := url.Parse(endpoint)
			if err != nil {
				logger.Error("Unable to parse endpoint", zap.Error(err))
				targetURL = awsURL
			}

			// Set req url to target endpoint
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.Host = targetURL.Host

			// Consume body and convert to io.ReadSeeker for signer to consume
			body, err := consume(req.Body)
			if err != nil {
				logger.Error("Unable to consume request body", zap.Error(err))

				// Forward unsigned request
				return
			}

			// Sign request. signer.Sign() also repopulates the request body.
			_, err = signer.Sign(req, body, serviceName, region, time.Now())
			if err != nil {
				logger.Error("Unable to sign request", zap.Error(err))
			}
		},
	}

	return &http.Server{
		Addr:              cfg.Endpoint,
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
	}, nil
}

// getServiceEndpoint returns X-Ray service endpoint.
// It is guaranteed that awsCfg config instance is non-nil and the region value is non nil or non empty in awsCfg object.
// Currently, the caller takes care of it.
func getServiceEndpoint(awsCfg *aws.Config, serviceName string) (string, error) {
	if isEmpty(awsCfg.Endpoint) {
		if isEmpty(awsCfg.Region) {
			return "", errors.New("unable to generate endpoint from region with nil value")
		}
		resolved, err := endpoints.DefaultResolver().EndpointFor(serviceName, *awsCfg.Region, setResolverConfig())
		return resolved.URL, err
	}
	return *awsCfg.Endpoint, nil
}

func isEmpty(val *string) bool {
	return val == nil || *val == ""
}

// consume readsAll() the body and creates a new io.ReadSeeker from the content. v4.Signer
// requires an io.ReadSeeker to be able to sign requests. May return a nil io.ReadSeeker.
func consume(body io.ReadCloser) (io.ReadSeeker, error) {
	var buf []byte

	// Return nil ReadSeeker if body is nil
	if body == nil {
		return nil, nil
	}

	// Consume body
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(buf), nil
}

func setResolverConfig() func(*endpoints.Options) {
	return func(p *endpoints.Options) {
		p.ResolveUnknownService = true
	}
}

// creates two maps: one mapping API names to their service configurations,
// and another mapping role ARNs to their corresponding AWS signers.
func buildRoutingMaps(routes []RoutingRule, defaultRoleARN string, defaultSigner *v4.Signer, defaultAWSEndpoint string, sessionCfg *awsutil.AWSSessionSettings, logger *zap.Logger) (map[string]*RoutingRule, map[string]*v4.Signer, error) {
	apiMap := make(map[string]*RoutingRule)
	signerMap := make(map[string]*v4.Signer)

	// Add default signer to map
	if defaultRoleARN != "" {
		signerMap[defaultRoleARN] = defaultSigner
	}

	for i := range routes {
		route := &routes[i]
		if route.ServiceName == "" {
			return nil, nil, fmt.Errorf("route[%d]: service_name is required", i)
		}

		if route.AWSEndpoint == "" {
			if defaultAWSEndpoint != "" {
				route.AWSEndpoint = defaultAWSEndpoint
			} else if route.Region != "" {
				resolved, err := getServiceEndpoint(&aws.Config{Region: &route.Region}, route.ServiceName)
				if err != nil {
					return nil, nil, fmt.Errorf("route[%d]: failed to resolve endpoint for service %s in region %s: %w", i, route.ServiceName, route.Region, err)
				}
				route.AWSEndpoint = resolved
			}
		}

		// Create signer for this role if it doesn't exist
		if route.RoleARN != "" {
			if _, exists := signerMap[route.RoleARN]; !exists {
				roleSessionCfg := *sessionCfg
				roleSessionCfg.RoleARN = route.RoleARN
				_, roleSess, err := awsutil.GetAWSConfigSession(logger, &awsutil.Conn{}, &roleSessionCfg)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to create session for role %s: %w", route.RoleARN, err)
				}
				signerMap[route.RoleARN] = &v4.Signer{
					Credentials: roleSess.Config.Credentials,
				}
			}
		}

		// Map request paths to service config.
		// Paths should be configured without a leading slash (e.g., "GetSamplingRules", "slos")
		// to match the trimmed request paths from the Director function.
		for _, path := range route.Paths {
			if _, exists := apiMap[path]; !exists {
				apiMap[path] = route
			}
		}
	}
	return apiMap, signerMap, nil
}
