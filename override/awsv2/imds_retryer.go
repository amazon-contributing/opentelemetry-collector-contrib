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

// IMDSRetryer extends retry.Standard to treat smithyhttp.ResponseError as retryable.
type IMDSRetryer struct {
	*retry.Standard
}

var _ aws.RetryerV2 = (*IMDSRetryer)(nil)

// NewIMDSRetryer allows us to retry IMDS errors
func NewIMDSRetryer(retries int) *IMDSRetryer {
	if retries < 0 {
		retries = 0
	}
	return &IMDSRetryer{
		Standard: retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = retries + 1 // MaxAttempts include the first attempt
		}),
	}
}

func (r *IMDSRetryer) IsErrorRetryable(err error) bool {
	// SDKv2 returns a ResponseError on request failure. Any of those errors is considered retryable.
	// https://github.com/aws/aws-sdk-go-v2/blob/dcbed91b6c6235022f15eda6ea526dbb91e1cb81/feature/ec2/imds/request_middleware.go#L185-L191
	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) || r.Standard.IsErrorRetryable(err)
}
