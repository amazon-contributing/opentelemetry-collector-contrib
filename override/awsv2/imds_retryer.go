// Copyright The OpenTelemetry Authors
// Portions of this file Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package awsv2 // import "github.com/amazon-contributing/opentelemetry-collector-contrib/override/awsv2"

import (
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// DefaultIMDSRetries is the recommended default retry count for NewIMDSRetryer.
const DefaultIMDSRetries = 1

// imdsRetryer extends retry.Standard to treat smithyhttp.ResponseError as retryable.
type imdsRetryer struct {
	*retry.Standard
}

var _ aws.RetryerV2 = (*imdsRetryer)(nil)

// NewIMDSRetryer returns an IMDS retryer with MaxAttempts = retries + 1. Negative values are clamped to 0.
// The retryer treats any smithyhttp.ResponseError as retryable. Otherwise behavior matches retry.Standard.
func NewIMDSRetryer(retries int) aws.RetryerV2 {
	retries = max(retries, 0)
	return &imdsRetryer{
		Standard: retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = retries + 1 // MaxAttempts include the first attempt
		}),
	}
}

func (r *imdsRetryer) IsErrorRetryable(err error) bool {
	// SDKv2 returns a ResponseError on request failure. Any of those errors is considered retryable.
	// https://github.com/aws/aws-sdk-go-v2/blob/dcbed91b6c6235022f15eda6ea526dbb91e1cb81/feature/ec2/imds/request_middleware.go#L185-L191
	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) || r.Standard.IsErrorRetryable(err)
}
