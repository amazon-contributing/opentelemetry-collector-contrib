// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package histograms // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/aws/cloudwatch/histograms"

import (
	"errors"
	"fmt"
	"math"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

func CheckValidity(dp pmetric.HistogramDataPoint) error {
	issues := []error{}

	bounds := dp.ExplicitBounds()
	bucketCounts := dp.BucketCounts()

	// Check counts length matches boundaries + 1
	// special case: no bucketCounts and no boundaries is still valid
	if bucketCounts.Len() != bounds.Len()+1 && bucketCounts.Len() != 0 && bounds.Len() != 0 {
		issues = append(issues, fmt.Errorf("bucket counts length (%d) doesn't match boundaries length (%d) + 1",
			bucketCounts.Len(), bounds.Len()))
	}

	if dp.HasMax() && dp.HasMin() && dp.Min() > dp.Max() {
		issues = append(issues, fmt.Errorf("min %f is greater than max %f", dp.Min(), dp.Max()))
	}

	if dp.HasMax() {
		if math.IsNaN(dp.Max()) {
			issues = append(issues, errors.New("max is NaN"))
		}
		if math.IsInf(dp.Max(), 0) {
			issues = append(issues, errors.New("max is +/-inf"))
		}
	}

	if dp.HasMin() {
		if math.IsNaN(dp.Min()) {
			issues = append(issues, errors.New("min is NaN"))
		}
		if math.IsInf(dp.Min(), 0) {
			issues = append(issues, errors.New("min is +/-inf"))
		}
	}

	if dp.HasSum() {
		if math.IsNaN(dp.Sum()) {
			issues = append(issues, errors.New("sum is NaN"))
		}
		if math.IsInf(dp.Sum(), 0) {
			issues = append(issues, errors.New("sum is +/-inf"))
		}
	}

	if bounds.Len() > 0 {
		// Check boundaries are in ascending order
		for i := 1; i < bounds.Len(); i++ {
			if bounds.At(i) <= bounds.At(i-1) {
				issues = append(issues, fmt.Errorf("boundaries not in ascending order: bucket index %d (%v) <= bucket index %d %v",
					i, bounds.At(i), i-1, bounds.At(i-1)))
			}
			if math.IsNaN(bounds.At(i)) {
				issues = append(issues, fmt.Errorf("boundary %d is NaN", i))
			}
			if math.IsInf(bounds.At(i), 0) {
				issues = append(issues, fmt.Errorf("boundary %d is +/-inf", i))
			}
		}
	}

	return errors.Join(issues...)
}
