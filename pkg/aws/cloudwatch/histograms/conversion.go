// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package histograms // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/aws/cloudwatch/histograms"

import (
	"math"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/aws/cloudwatch"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type ExponentialMapping struct {
	maximum     float64
	minimum     float64
	sampleCount float64
	sum         float64
	values      []float64
	counts      []float64
}

var _ (cloudwatch.HistogramDataPoint) = (*ExponentialMapping)(nil)

// ConvertOTelToCloudWatch converts an OpenTelemetry histogram datapoint to a CloudWatch histogram datapoint using
// exponential mapping
func ConvertOTelToCloudWatch(dp pmetric.HistogramDataPoint) cloudwatch.HistogramDataPoint {
	// maximumInnerBucketCount is the maximum number of inner buckets that each outer bucket can be represented with
	//
	// A larger values increase the resolution at which the data is sub-sampled while also incurring additional memory
	// allocation, processing time, and the maximum number of value/count pairs that are sent to CloudWatch which could
	// cause a CloudWatch PutMetricData / PutLogEvent request to be split into multiple requests due to the 100/150
	// metric datapoint limit.
	const maximumInnerBucketCount = 10

	// No validations - assuming valid input histogram

	em := &ExponentialMapping{
		maximum:     dp.Max(),
		minimum:     dp.Min(),
		sampleCount: float64(dp.Count()),
		sum:         dp.Sum(),
	}

	// bounds specifies the boundaries between buckets
	// bucketCounts specifies the number of datapoints in each bucket
	// there is always 1 more bucket count than there is boundaries
	// len(bucketCounts) = len(bounds) + 1
	bounds := dp.ExplicitBounds()
	lenBounds := bounds.Len()
	bucketCounts := dp.BucketCounts()
	lenBucketCounts := bucketCounts.Len()

	// Special case: no boundaries implies a single bucket
	if lenBounds == 0 {
		em.counts = append(em.counts, float64(bucketCounts.At(0))) // recall that len(bucketCounts) = len(bounds)+1
		if dp.HasMax() && dp.HasMin() {
			em.values = append(em.values, em.minimum/2.0+em.maximum/2.0)
		} else if dp.HasMax() {
			em.values = append(em.values, em.maximum) // only data point we have is the maximum
		} else if dp.HasMin() {
			em.values = append(em.values, em.minimum) // only data point we have is the minimum
		} else {
			em.values = append(em.values, 0) // arbitrary value
		}
		return em
	}

	// To create inner buckets, all outer buckets need to have defined boundaries. The first and last bucket use the
	// min and max and their lower and upper bounds respectively. The min and max are optional on the OTel datapoint.
	// When min and max are not defined, make some reasonable about about what the min/max could be
	if !dp.HasMin() {

		// Find the first bucket which contains some data points. The min must be in that bucket
		minBucketIdx := 0
		for i := 0; i < lenBucketCounts; i++ {
			if bucketCounts.At(i) > 0 {
				minBucketIdx = i
				break
			}
		}

		// take the lower bound of the bucket. lower bound of bucket index n is boundary index n-1
		if minBucketIdx != 0 {
			em.minimum = bounds.At(minBucketIdx - 1)
		} else {
			bucketWidth := 0.001 // arbitrary width - there's no information about this histogram to make an inference with if there are no bounds
			if lenBounds > 1 {
				bucketWidth = bounds.At(1) - bounds.At(0)
			}
			em.minimum = bounds.At(0) - bucketWidth

			// if all boundaries are positive, assume all data is positive. this covers use cases where Prometheus
			// histogram metrics for non-zero values like request durations have their first bucket start at 0. for
			// these metrics, a negative minimum will cause percentile metrics to be unavailable
			if bounds.At(0) >= 0 {
				em.minimum = max(em.minimum, 0.0)
			}
		}

	}

	if !dp.HasMax() {

		// Find the last bucket with some data in it. The max must be in that bucket
		maxBucketIdx := lenBounds - 1
		for i := lenBucketCounts - 1; i >= 0; i-- {
			if bucketCounts.At(i) > 0 {
				maxBucketIdx = i
				break
			}
		}

		// we want the upper bound of the bucket. the upper bound of bucket index n is boundary index n
		if maxBucketIdx <= lenBounds-1 {
			em.maximum = bounds.At(maxBucketIdx)
		} else {
			bucketWidth := 0.01 // arbitrary width - there's no information about this histogram to make an inference with
			if lenBounds > 1 {
				bucketWidth = bounds.At(lenBounds-1) - bounds.At(lenBounds-2)
			}
			em.maximum = bounds.At(lenBounds-1) + bucketWidth
		}

	}

	// Pre-calculate total output size to avoid dynamic growth
	totalOutputSize := 0
	for i := 0; i < lenBucketCounts; i++ {
		sampleCount := bucketCounts.At(i)
		if sampleCount > 0 {
			totalOutputSize += int(min(sampleCount, maximumInnerBucketCount))
		}
	}
	if totalOutputSize == 0 {
		// No samples in any bucket
		return em
	}

	em.values = make([]float64, 0, totalOutputSize)
	em.counts = make([]float64, 0, totalOutputSize)

	for i := 0; i < lenBucketCounts; i++ {
		sampleCount := int(bucketCounts.At(i))
		if sampleCount == 0 {
			// No need to operate on a bucket with no samples
			continue
		}

		lowerBound := em.minimum
		if i > 0 {
			lowerBound = bounds.At(i - 1)
		}
		upperBound := em.maximum
		if i < lenBucketCounts-1 {
			upperBound = bounds.At(i)
		}

		// This algorithm creates "inner buckets" between user-defined bucket based on the sample count, up to a
		// maximum. A logarithmic ratio (named "magnitude") compares the density between the current bucket and the
		// next bucket. This logarithmic ratio is used to decide how to spread samples amongst inner buckets.
		//
		// case 1: magnitude < 0
		//   * What this means: Current bucket is denser than the next bucket -> density is decreasing.
		//   * What we do: Use inverse quadratic distribution to spread the samples. This allocates more samples towards
		//     the lower bound of the bucket.
		// case 2: 0 <= magnitude < 1
		//   * What this means: Current bucket and next bucket has similar densities -> density is not changing much.
		//   * What we do: Use inform distribution to spread the samples. Extra samples that can't be spread evenly are
		//     (arbitrarily) allocated towards the start of the bucket.
		// case 3: 1 <= magnitude
		//   * What this means: Current bucket is less dense than the next bucket -> density is increasing.
		//   * What we do: Use quadratic distribution to spread the samples. This allocates more samples toward the end
		//     of the bucket.
		//
		// As a small optimization, we omit the logarithm invocation and change the thresholds.
		ratio := 0.0
		if i < lenBucketCounts-1 {
			nextSampleCount := bucketCounts.At(i + 1)
			// If next bucket is empty, than density is surely decreasing
			if nextSampleCount == 0 {
				ratio = 0.0
			} else {
				var nextUpperBound float64
				if i+1 == lenBucketCounts-1 {
					nextUpperBound = em.maximum
				} else {
					nextUpperBound = bounds.At(i + 1)
				}

				//currentBucketDensity := float64(sampleCount) / (upperBound - lowerBound)
				//nextBucketDensity := float64(nextSampleCount) / (nextUpperBound - upperBound)
				//ratio = nextBucketDensity / currentBucketDensity

				// the following calculations are the same but improves speed by ~1% in benchmark tests
				denom := (nextUpperBound - upperBound) * float64(sampleCount)
				numerator := (upperBound - lowerBound) * float64(nextSampleCount)
				ratio = numerator / denom
			}
		}

		// innerBucketCount is how many "inner buckets" to spread the sample count amongst
		innerBucketCount := min(sampleCount, maximumInnerBucketCount)
		delta := (upperBound - lowerBound) / float64(innerBucketCount)

		if ratio < 1.0/math.E { // magnitude < 0: Use -yx^2 (inverse quadratic)
			sigma := float64(sumOfSquares(innerBucketCount))
			epsilon := float64(sampleCount) / sigma
			entryStart := len(em.counts)

			runningSum := 0
			for j := 0; j < innerBucketCount; j++ {
				innerBucketSampleCount := epsilon * float64((j-innerBucketCount)*(j-innerBucketCount))
				innerBucketSampleCountAdjusted := int(math.Floor(innerBucketSampleCount))
				if innerBucketSampleCountAdjusted > 0 {
					runningSum += innerBucketSampleCountAdjusted
					em.values = append(em.values, lowerBound+delta*float64(j+1))
					em.counts = append(em.counts, float64(innerBucketSampleCountAdjusted))
				}
			}

			// distribute the remainder towards the front
			remainder := sampleCount - runningSum
			// make sure there's room for the remainder
			if len(em.counts) < entryStart+remainder {
				em.counts = append(em.counts, make([]float64, remainder)...)
				em.values = append(em.values, make([]float64, remainder)...)
			}
			for j := 0; j < remainder; j++ {
				em.counts[entryStart] += 1
				entryStart += 1
			}

		} else if ratio < math.E { // 0 <= magnitude < 1: Use x
			// Distribute samples evenly with integer counts
			baseCount := sampleCount / innerBucketCount
			remainder := sampleCount % innerBucketCount
			for j := 1; j <= innerBucketCount; j++ {
				count := baseCount

				// Distribute remainder to first few buckets
				if j <= remainder {
					count++
				}
				em.values = append(em.values, lowerBound+delta*float64(j))
				em.counts = append(em.counts, float64(count))
			}

		} else { // magnitude >= 1: Use yx^2 (quadratic)
			sigma := float64(sumOfSquares(innerBucketCount))
			epsilon := float64(sampleCount) / sigma
			entryStart := len(em.counts)

			runningSum := 0
			for j := 1; j <= innerBucketCount; j++ {
				innerBucketSampleCount := epsilon * float64(j*j)
				innerBucketSampleCountAdjusted := int(math.Floor(innerBucketSampleCount))
				if innerBucketSampleCountAdjusted > 0 {
					runningSum += innerBucketSampleCountAdjusted
					em.values = append(em.values, lowerBound+delta*float64(j))
					em.counts = append(em.counts, float64(innerBucketSampleCountAdjusted))
				}
			}

			// distribute the remainder towards the end
			remainder := sampleCount - runningSum
			// make sure there's room for the remainder
			if len(em.counts) < entryStart+remainder {
				em.counts = append(em.counts, make([]float64, remainder)...)
				em.values = append(em.values, make([]float64, remainder)...)
			}
			entryStart = len(em.counts) - 1
			for j := 0; j < remainder; j++ {
				em.counts[entryStart] += 1
				entryStart -= 1
			}
		}

	}

	// Move last entry to maximum if needed
	if dp.HasMax() && len(em.values) > 0 {
		lastIdx := len(em.values) - 1
		for i := lastIdx; i >= 0; i-- {
			if em.counts[i] > 0 {
				lastIdx = i
				break
			}
		}
		em.values[lastIdx] = em.maximum
		em.values = em.values[:lastIdx+1]
		em.counts = em.counts[:lastIdx+1]
	}

	return em
}

func (em *ExponentialMapping) ValuesAndCounts() ([]float64, []float64) {
	return em.values, em.counts
}

func (em *ExponentialMapping) Minimum() float64 {
	return em.minimum
}

func (em *ExponentialMapping) Maximum() float64 {
	return em.maximum
}

func (em *ExponentialMapping) SampleCount() float64 {
	return em.sampleCount
}

func (em *ExponentialMapping) Sum() float64 {
	return em.sum
}

// sumOfSquares is a closed form calculation of Σx^2, for 1 to n
func sumOfSquares(n int) int {
	return n * (n + 1) * (2*n + 1) / 6
}
