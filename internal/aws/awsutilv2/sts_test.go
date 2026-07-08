// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsutilv2

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStsCredentialsProvider_Retrieve(t *testing.T) {
	t.Run("Regional/Success", func(t *testing.T) {
		regional := new(mockCredentialsProvider)
		regional.On("Retrieve", t.Context()).Return(testCredentials, nil).Once()
		partitional := new(mockCredentialsProvider)

		provider := &stsCredentialsProvider{
			regional:    regional,
			partitional: partitional,
		}

		got, err := provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, testCredentials, got)
		regional.AssertExpectations(t)
		partitional.AssertNotCalled(t, "Retrieve", t.Context())
	})

	t.Run("Regional/OtherError", func(t *testing.T) {
		regional := new(mockCredentialsProvider)
		regional.On("Retrieve", t.Context()).Return(aws.Credentials{}, assert.AnError).Once()
		partitional := new(mockCredentialsProvider)
		provider := &stsCredentialsProvider{
			regional:    regional,
			partitional: partitional,
		}

		_, err := provider.Retrieve(t.Context())
		assert.Equal(t, assert.AnError, err)
		regional.AssertExpectations(t)
		partitional.AssertNotCalled(t, "Retrieve", t.Context())
	})

	t.Run("Fallback/RegionDisabledException", func(t *testing.T) {
		regional := new(mockCredentialsProvider)
		regional.On("Retrieve", t.Context()).Return(aws.Credentials{}, &types.RegionDisabledException{}).Once()
		partitional := new(mockCredentialsProvider)
		partitional.On("Retrieve", t.Context()).Return(testCredentials, nil).Times(2)

		provider := &stsCredentialsProvider{
			regional:    regional,
			partitional: partitional,
		}

		assert.Nil(t, provider.fallback.Load())

		got, err := provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, testCredentials, got)
		assert.NotNil(t, provider.fallback.Load())

		// Second call should use fallback directly.
		got, err = provider.Retrieve(t.Context())
		require.NoError(t, err)
		assert.Equal(t, testCredentials, got)

		regional.AssertExpectations(t)
		partitional.AssertExpectations(t)
	})

	t.Run("Fallback/NoPartitional", func(t *testing.T) {
		orig := getPartitionPrimaryRegion
		t.Cleanup(func() { getPartitionPrimaryRegion = orig })
		getPartitionPrimaryRegion = func(string) string { return "" }

		provider := newRegionalFallbackCredentialsProvider(aws.Config{}, testRegion, func(cfg aws.Config) aws.CredentialsProvider {
			return stscreds.NewAssumeRoleProvider(newAssumeRoleClient(cfg), testRoleARN)
		})
		require.NotNil(t, provider)
		_, wrapped := provider.(*stsCredentialsProvider)
		assert.False(t, wrapped)
		_, ok := provider.(*stscreds.AssumeRoleProvider)
		assert.True(t, ok)
	})
}

func TestNewRegionalCredentialsProvider(t *testing.T) {
	provider := newRegionalFallbackCredentialsProvider(aws.Config{}, testRegion, func(cfg aws.Config) aws.CredentialsProvider {
		return stscreds.NewAssumeRoleProvider(newAssumeRoleClient(cfg), testRoleARN)
	})

	assert.NotNil(t, provider)
	stsProvider, ok := provider.(*stsCredentialsProvider)
	require.True(t, ok)
	assert.NotNil(t, stsProvider.regional)
	assert.NotNil(t, stsProvider.partitional)

	_, ok = stsProvider.regional.(*stscreds.AssumeRoleProvider)
	require.True(t, ok)
}

func TestConfusedDeputyHeaders(t *testing.T) {
	testCases := map[string]struct {
		envSourceArn          string
		envSourceAccount      string
		expectedHeaderArn     string
		expectedHeaderAccount string
	}{
		"Unpopulated": {
			envSourceArn: "", envSourceAccount: "",
			expectedHeaderArn: "", expectedHeaderAccount: "",
		},
		"BothPopulated": {
			envSourceArn: testInstanceARN, envSourceAccount: testAccountID,
			expectedHeaderArn: testInstanceARN, expectedHeaderAccount: testAccountID,
		},
		"OnlySourceArn": {
			envSourceArn: testInstanceARN, envSourceAccount: "",
			expectedHeaderArn: "", expectedHeaderAccount: "",
		},
		"OnlySourceAccount": {
			envSourceArn: "", envSourceAccount: testAccountID,
			expectedHeaderArn: "", expectedHeaderAccount: "",
		},
	}
	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envSourceAccount, testCase.envSourceAccount)
			t.Setenv(envSourceArn, testCase.envSourceArn)

			client := newStsClient(testAWSConfig)

			// Don't actually make the AssumeRole call. Verify the headers via a finalize middleware that intercepts
			// the request and short-circuits.
			input := &sts.AssumeRoleInput{
				RoleArn:         aws.String(testRoleARN),
				RoleSessionName: aws.String("MockSession"),
			}

			var capturedHeaders http.Header
			_, err := client.AssumeRole(t.Context(), input, func(o *sts.Options) {
				o.APIOptions = append(o.APIOptions, func(s *middleware.Stack) error {
					return s.Finalize.Add(middleware.FinalizeMiddlewareFunc("CaptureHeaders",
						func(_ context.Context, in middleware.FinalizeInput, _ middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
							if req, ok := in.Request.(*smithyhttp.Request); ok {
								capturedHeaders = req.Header.Clone()
							}
							return middleware.FinalizeOutput{Result: &sts.AssumeRoleOutput{}}, middleware.Metadata{}, nil
						}), middleware.After)
				})
			})
			require.NoError(t, err)

			assert.Equal(t, testCase.expectedHeaderArn, capturedHeaders.Get(sourceArnHeaderKey))
			assert.Equal(t, testCase.expectedHeaderAccount, capturedHeaders.Get(sourceAccountHeaderKey))
		})
	}
}

func TestConfusedDeputyHeaders_NonHTTPRequest(t *testing.T) {
	m := &confusedDeputyHeaders{sourceArn: testInstanceARN, sourceAccount: testAccountID}

	input := middleware.BuildInput{Request: "not-an-http-request"}
	next := middleware.BuildHandlerFunc(func(_ context.Context, _ middleware.BuildInput) (middleware.BuildOutput, middleware.Metadata, error) {
		t.Fatalf("next must not be invoked when type assertion fails")
		return middleware.BuildOutput{}, middleware.Metadata{}, nil
	})

	_, _, err := m.HandleBuild(t.Context(), input, next)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unrecognized transport type")
}
