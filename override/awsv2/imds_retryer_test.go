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
		"Nil": {
			err:  nil,
			want: false,
		},
		"ResponseError4xx": {
			err:  smithyResponseError(http.StatusNotFound),
			want: true,
		},
		"ResponseError5xx": {
			err:  smithyResponseError(http.StatusInternalServerError),
			want: true,
		},
		"WrappedResponseError": {
			err: errors.Join(
				errors.New("outer error"),
				smithyResponseError(http.StatusServiceUnavailable),
			),
			want: true,
		},
		"GenericError": {
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
		"PositiveRetries": {
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

// smithyResponseError builds a *smithyhttp.ResponseError carrying the given HTTP status code.
func smithyResponseError(statusCode int) *smithyhttp.ResponseError {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{StatusCode: statusCode},
		},
		Err: errors.New("request to EC2 IMDS failed"),
	}
}
