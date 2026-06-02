// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsv2

import (
	"errors"
	"net/http"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
)

func TestIMDSRetryer_IsErrorRetryable(t *testing.T) {
	testCases := map[string]struct {
		err  error
		want bool
	}{
		"ErrorIsNilNotRetryable": {
			err:  nil,
			want: false,
		},
		"ErrorIsIMDSResponseErrorRetryable": {
			err: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{
					Response: &http.Response{
						StatusCode: http.StatusNotFound,
					},
				},
				Err: errors.New("request to EC2 IMDS failed"),
			},
			want: true,
		},
		"ErrorIsIMDSResponseError5xxRetryable": {
			err: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{
					Response: &http.Response{
						StatusCode: http.StatusInternalServerError,
					},
				},
				Err: errors.New("request to EC2 IMDS failed"),
			},
			want: true,
		},
		"ErrorIsWrappedIMDSResponseErrorRetryable": {
			err: errors.Join(
				errors.New("outer error"),
				&smithyhttp.ResponseError{
					Response: &smithyhttp.Response{
						Response: &http.Response{
							StatusCode: http.StatusServiceUnavailable,
						},
					},
					Err: errors.New("request to EC2 IMDS failed"),
				},
			),
			want: true,
		},
		"ErrorIsGenericErrorNotRetryableByDefault": {
			err:  errors.New("some other error"),
			want: false, // Standard retryer doesn't treat generic errors as retryable by default
		},
	}

	retryer := NewIMDSRetryer(DefaultIMDSRetries)

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			got := retryer.IsErrorRetryable(testCase.err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestIMDSRetryer_MaxAttempts(t *testing.T) {
	testCases := map[string]struct {
		retries int
		want    int
	}{
		"DefaultRetries": {
			retries: DefaultIMDSRetries,
			want:    DefaultIMDSRetries + 1,
		},
		"TwoRetries": {
			retries: 2,
			want:    3,
		},
		"ZeroRetries": {
			retries: 0,
			want:    1,
		},
		"NegativeRetries": {
			retries: -2,
			want:    1,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			retryer := NewIMDSRetryer(testCase.retries)
			assert.Equal(t, testCase.want, retryer.MaxAttempts())
		})
	}
}
