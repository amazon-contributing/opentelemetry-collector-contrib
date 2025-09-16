// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package histograms

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHistogramFeasibility(t *testing.T) {
	testCases := TestCases()
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			feasible, reason := checkFeasibility(tc.Input)
			assert.True(t, feasible, reason)

			// check that the test case percentile ranges are valid
			for percentile, expectedRange := range tc.Expected.PercentileRanges {
				calculatedLow, calculatedHigh := calculatePercentileRange(tc.Input, percentile)
				assert.Equal(t, expectedRange.Low, calculatedLow, "calculated low does not match expected low for percentile %v", percentile)
				assert.Equal(t, expectedRange.High, calculatedHigh, "calculated high does not match expected high for percentile %v", percentile)
			}

			assertOptionalFloat(t, "min", tc.Expected.Min, tc.Input.Min)
			assertOptionalFloat(t, "max", tc.Expected.Max, tc.Input.Max)
		})
	}
}

func TestInvalidHistogramFeasibility(t *testing.T) {
	invalidTestCases := InvalidTestCases()

	for _, tc := range invalidTestCases {
		t.Run(tc.Name, func(t *testing.T) {
			feasible, reason := checkFeasibility(tc.Input)
			assert.False(t, feasible, reason)
		})
	}
}

func TestVisualizeHistograms(t *testing.T) {
	// comment the next line to visualize the input histograms
	t.Skip("Skip visualization test")
	testCases := TestCases()
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// The large bucket tests are just too big to output
			if matched, _ := regexp.MatchString("\\d\\d\\d Buckets", tc.Name); matched {
				return
			}
			visualizeHistogramWithPercentiles(tc.Input)
		})
	}
}

func checkFeasibility(hi HistogramInput) (bool, string) {

	// Special case: empty histogram is valid
	if len(hi.Boundaries) == 0 && len(hi.Counts) == 0 {
		return true, ""
	}

	// Check counts length matches boundaries + 1
	if len(hi.Counts) != len(hi.Boundaries)+1 {
		return false, "Can't have counts without boundaries"
	}

	if hi.Max != nil && hi.Min != nil && *hi.Min > *hi.Max {
		return false, fmt.Sprintf("min %f is greater than max %f", *hi.Min, *hi.Max)
	}

	// Rest of checks only apply if we have boundaries/counts
	if len(hi.Boundaries) > 0 || len(hi.Counts) > 0 {
		// Check boundaries are in ascending order
		for i := 1; i < len(hi.Boundaries); i++ {
			if hi.Boundaries[i] <= hi.Boundaries[i-1] {
				return false, fmt.Sprintf("boundaries not in ascending order: %v <= %v",
					hi.Boundaries[i], hi.Boundaries[i-1])
			}
		}

		// Check counts array length
		if len(hi.Counts) != len(hi.Boundaries)+1 {
			return false, fmt.Sprintf("counts length (%d) should be boundaries length (%d) + 1",
				len(hi.Counts), len(hi.Boundaries))
		}

		// Verify total count matches sum of bucket counts
		var totalCount uint64
		for _, count := range hi.Counts {
			totalCount += count
		}
		if totalCount != hi.Count {
			return false, fmt.Sprintf("sum of counts (%d) doesn't match total count (%d)",
				totalCount, hi.Count)
		}

		// Check min/max feasibility if defined
		if hi.Min != nil {
			// If there are boundaries, first bucket must have counts > 0 only if min <= first boundary
			if len(hi.Boundaries) > 0 && hi.Counts[0] > 0 && *hi.Min > hi.Boundaries[0] {
				return false, fmt.Sprintf("min (%v) > first boundary (%v) but first bucket has counts",
					*hi.Min, hi.Boundaries[0])
			}
		}

		if hi.Max != nil {
			// If there are boundaries, last bucket must have counts > 0 only if max > last boundary
			if len(hi.Boundaries) > 0 && hi.Counts[len(hi.Counts)-1] > 0 &&
				*hi.Max <= hi.Boundaries[len(hi.Boundaries)-1] {
				return false, fmt.Sprintf("max (%v) <= last boundary (%v) but overflow bucket has counts",
					*hi.Max, hi.Boundaries[len(hi.Boundaries)-1])
			}
		}

		// Check sum feasibility
		if len(hi.Boundaries) > 0 {
			// Calculate minimum possible sum
			minSum := float64(0)
			if hi.Min != nil {
				// Find which bucket the minimum value belongs to
				minBucket := 0
				for i, bound := range hi.Boundaries {
					if *hi.Min > bound {
						minBucket = i + 1
					}
				}
				// Apply min value only from its containing bucket
				for i := minBucket; i < len(hi.Counts); i++ {
					if i == minBucket {
						minSum += float64(hi.Counts[i]) * *hi.Min
					} else {
						minSum += float64(hi.Counts[i]) * hi.Boundaries[i-1]
					}
				}
			} else {
				// Without min, use lower bounds
				for i := 1; i < len(hi.Counts); i++ {
					minSum += float64(hi.Counts[i]) * hi.Boundaries[i-1]
				}
			}

			// Calculate maximum possible sum
			maxSum := float64(0)
			if hi.Max != nil {
				// Find which bucket the maximum value belongs to
				maxBucket := len(hi.Boundaries) // Default to overflow bucket
				for i, bound := range hi.Boundaries {
					if *hi.Max <= bound {
						maxBucket = i
						break
					}
				}
				// Apply max value only up to its containing bucket
				for i := 0; i < len(hi.Counts); i++ {
					if i > maxBucket {
						maxSum += float64(hi.Counts[i]) * *hi.Max
					} else if i == len(hi.Boundaries) {
						maxSum += float64(hi.Counts[i]) * *hi.Max
					} else {
						maxSum += float64(hi.Counts[i]) * hi.Boundaries[i]
					}
				}
			} else {
				// If no max defined, we can't verify upper bound
				maxSum = math.Inf(1)
			}

			if hi.Sum < minSum {
				return false, fmt.Sprintf("sum (%v) is less than minimum possible sum (%v)",
					hi.Sum, minSum)
			}
			if maxSum != math.Inf(1) && hi.Sum > maxSum {
				return false, fmt.Sprintf("sum (%v) is greater than maximum possible sum (%v)",
					hi.Sum, maxSum)
			}
		}
	}

	return true, ""
}

func calculatePercentileRange(hi HistogramInput, percentile float64) (float64, float64) {
	if len(hi.Boundaries) == 0 {
		// No buckets - use min/max if available
		if hi.Min != nil && hi.Max != nil {
			return *hi.Min, *hi.Max
		}
		return math.Inf(-1), math.Inf(1)
	}

	percentilePosition := uint64(float64(hi.Count) * percentile)
	var cumulativeCount uint64

	// Find which bucket contains the percentile
	for i, count := range hi.Counts {
		cumulativeCount += count
		if cumulativeCount > percentilePosition {
			// Found the bucket containing the percentile
			if i == 0 {
				// First bucket: (-inf, bounds[0]]
				if hi.Min != nil {
					return *hi.Min, hi.Boundaries[0]
				}
				return math.Inf(-1), hi.Boundaries[0]
			} else if i == len(hi.Boundaries) {
				// Last bucket: (bounds[last], +inf)
				if hi.Max != nil {
					return hi.Boundaries[i-1], *hi.Max
				}
				return hi.Boundaries[i-1], math.Inf(1)
			} else {
				// Middle bucket: (bounds[i-1], bounds[i]]
				return hi.Boundaries[i-1], hi.Boundaries[i]
			}
		}
	}
	return 0, 0 // Should never reach here for valid histograms
}

func assertOptionalFloat(t *testing.T, name string, expected, actual *float64) {
	if expected != nil {
		assert.NotNil(t, actual, "Expected %s defined but not defined on input", name)
		if actual != nil {
			assert.Equal(t, expected, actual)
		}
	} else {
		assert.Nil(t, actual, "Input %s defined but no %s is expected", name, name)
	}
}

func visualizeHistogramWithPercentiles(hi HistogramInput) {
	fmt.Printf("\nHistogram Visualization with Percentiles\n")
	fmt.Printf("Count: %d, Sum: %.2f\n", hi.Count, hi.Sum)
	if hi.Min != nil {
		fmt.Printf("Min: %.2f ", *hi.Min)
	}
	if hi.Max != nil {
		fmt.Printf("Max: %.2f", *hi.Max)
	}
	fmt.Println()

	if len(hi.Boundaries) == 0 {
		fmt.Println("No buckets defined")
		return
	}

	// Calculate cumulative counts for CDF
	cumulativeCounts := make([]uint64, len(hi.Counts))
	var total uint64
	for i, count := range hi.Counts {
		total += count
		cumulativeCounts[i] = total
	}

	// Find percentile positions
	percentiles := []float64{0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99}
	percentilePositions := make(map[float64]int)
	for _, p := range percentiles {
		pos := uint64(float64(hi.Count) * p)
		for i, cumCount := range cumulativeCounts {
			if cumCount > pos {
				percentilePositions[p] = i
				break
			}
		}
	}

	maxCount := uint64(0)
	for _, count := range hi.Counts {
		if count > maxCount {
			maxCount = count
		}
	}

	fmt.Println("\nHistogram:")
	for i, count := range hi.Counts {
		var bucketLabel string
		if i == 0 {
			if hi.Min != nil {
				bucketLabel = fmt.Sprintf("(%.2f, %.1f]", *hi.Min, hi.Boundaries[0])
			} else {
				bucketLabel = fmt.Sprintf("(-∞, %.1f]", hi.Boundaries[0])
			}
		} else if i == len(hi.Boundaries) {
			if hi.Max != nil {
				bucketLabel = fmt.Sprintf("(%.1f, %.2f]", hi.Boundaries[i-1], *hi.Max)
			} else {
				bucketLabel = fmt.Sprintf("(%.1f, +∞)", hi.Boundaries[i-1])
			}
		} else {
			bucketLabel = fmt.Sprintf("(%.1f, %.1f]", hi.Boundaries[i-1], hi.Boundaries[i])
		}

		barLength := int(float64(count) / float64(maxCount) * 40)
		bar := strings.Repeat("█", barLength)

		// Mark percentile buckets
		percentileMarkers := ""
		for _, p := range percentiles {
			if percentilePositions[p] == i {
				percentileMarkers += fmt.Sprintf(" P%.0f", p*100)
			}
		}

		fmt.Printf("%-30s %4d |%s%s\n", bucketLabel, count, bar, percentileMarkers)
	}

	fmt.Println("\nCumulative Distribution (CDF):")
	for i, cumCount := range cumulativeCounts {
		var bucketLabel string
		if i == 0 {
			bucketLabel = fmt.Sprintf("≤ %.1f", hi.Boundaries[0])
		} else if i == len(hi.Boundaries) {
			bucketLabel = "≤ +∞"
		} else {
			bucketLabel = fmt.Sprintf("≤ %.1f", hi.Boundaries[i])
		}

		cdfPercent := float64(cumCount) / float64(hi.Count) * 100
		cdfBarLength := int(cdfPercent / 100 * 40)
		cdfBar := strings.Repeat("▓", cdfBarLength)

		// Add percentile lines
		percentileLines := ""
		for _, p := range percentiles {
			if percentilePositions[p] == i {
				percentileLines += fmt.Sprintf(" ──P%.0f", p*100)
			}
		}

		fmt.Printf("%-15s %6.1f%% |%s%s\n", bucketLabel, cdfPercent, cdfBar, percentileLines)
	}

	// Show percentile ranges
	fmt.Println("\nPercentile Ranges:")
	for _, p := range percentiles {
		low, high := calculatePercentileRange(hi, p)
		fmt.Printf("P%.0f: [%.2f, %.2f]\n", p*100, low, high)
	}
}
