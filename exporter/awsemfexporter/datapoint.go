// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter"

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"

	aws "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/metrics"
)

const (
	summaryCountSuffix = "_count"
	summarySumSuffix   = "_sum"
)

type emfCalculators struct {
	delta   aws.MetricCalculator
	summary aws.MetricCalculator
}

func calculateSummaryDelta(prev *aws.MetricValue, val any, _ time.Time) (any, bool) {
	metricEntry := val.(summaryMetricEntry)
	summaryDelta := metricEntry.sum
	countDelta := metricEntry.count
	if prev != nil {
		prevSummaryEntry := prev.RawValue.(summaryMetricEntry)
		summaryDelta = metricEntry.sum - prevSummaryEntry.sum
		countDelta = metricEntry.count - prevSummaryEntry.count
	} else {
		return summaryMetricEntry{summaryDelta, countDelta}, false
	}
	return summaryMetricEntry{summaryDelta, countDelta}, true
}

// dataPoint represents a processed metric data point
type dataPoint struct {
	name        string
	value       any
	labels      map[string]string
	timestampMs int64
}

// dataPoints is a wrapper interface for:
//   - pmetric.NumberDataPointSlice
//   - pmetric.HistogramDataPointSlice
//   - pmetric.SummaryDataPointSlice
type dataPoints interface {
	Len() int
	// CalculateDeltaDatapoints calculates the delta datapoint from the DataPointSlice at i-th index
	// for some type (Counter, Summary)
	// dataPoint: the adjusted data point
	// retained: indicates whether the data point is valid for further process
	// NOTE: It is an expensive call as it calculates the metric value.
	CalculateDeltaDatapoints(i int, instrumentationScopeName string, detailedMetrics bool, calculators *emfCalculators) (dataPoint []dataPoint, retained bool)
	// IsStaleNaNInf returns true if metric value has NoRecordedValue flag set or if any metric value contains a NaN or Inf.
	// When return value is true, IsStaleNaNInf also returns the attributes attached to the metric which can be used for
	// logging purposes.
	IsStaleNaNInf(i int) (bool, pcommon.Map)
}

// deltaMetricMetadata contains the metadata required to perform rate/delta calculation
type deltaMetricMetadata struct {
	adjustToDelta              bool
	retainInitialValueForDelta bool
	metricName                 string
	namespace                  string
	logGroup                   string
	logStream                  string
}

// numberDataPointSlice is a wrapper for pmetric.NumberDataPointSlice
type numberDataPointSlice struct {
	deltaMetricMetadata
	pmetric.NumberDataPointSlice
}

// histogramDataPointSlice is a wrapper for pmetric.HistogramDataPointSlice
type histogramDataPointSlice struct {
	deltaMetricMetadata
	pmetric.HistogramDataPointSlice
}

type exponentialHistogramDataPointSlice struct {
	deltaMetricMetadata
	pmetric.ExponentialHistogramDataPointSlice
}

// summaryDataPointSlice is a wrapper for pmetric.SummaryDataPointSlice
type summaryDataPointSlice struct {
	deltaMetricMetadata
	pmetric.SummaryDataPointSlice
}

type summaryMetricEntry struct {
	sum   float64
	count uint64
}

type dataPointSplit struct {
	values     []float64
	counts     []float64
	count      int
	length     int
	bucketMin  float64
	bucketMax  float64
	maxBuckets int
}

//func newDataPointSplit(count int, length int, bucketMin float64, bucketMax float64, maxBuckets int) *dataPointSplit {
//	return &dataPointSplit{
//		values:     []float64{},
//		counts:     []float64{},
//		count:      count,
//		length:     length,
//		bucketMin:  bucketMin,
//		bucketMax:  bucketMax,
//		maxBuckets: maxBuckets,
//	}
//}

// CalculateDeltaDatapoints retrieves the NumberDataPoint at the given index and performs rate/delta calculation if necessary.
func (dps numberDataPointSlice) CalculateDeltaDatapoints(i int, _ string, _ bool, calculators *emfCalculators) ([]dataPoint, bool) {
	metric := dps.NumberDataPointSlice.At(i)
	labels := createLabels(metric.Attributes())
	timestampMs := unixNanoToMilliseconds(metric.Timestamp())

	var metricVal float64
	switch metric.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		metricVal = metric.DoubleValue()
	case pmetric.NumberDataPointValueTypeInt:
		metricVal = float64(metric.IntValue())
	}

	retained := true

	if dps.adjustToDelta {
		var deltaVal any
		mKey := aws.NewKey(dps.deltaMetricMetadata, labels)
		deltaVal, retained = calculators.delta.Calculate(mKey, metricVal, metric.Timestamp().AsTime())

		// If a delta to the previous data point could not be computed use the current metric value instead
		if !retained && dps.retainInitialValueForDelta {
			retained = true
			deltaVal = metricVal
		}

		if !retained {
			return nil, retained
		}
		// It should not happen in practice that the previous metric value is smaller than the current one.
		// If it happens, we assume that the metric is reset for some reason.
		if deltaVal.(float64) >= 0 {
			metricVal = deltaVal.(float64)
		}
	}

	return []dataPoint{{name: dps.metricName, value: metricVal, labels: labels, timestampMs: timestampMs}}, retained
}

func (dps numberDataPointSlice) IsStaleNaNInf(i int) (bool, pcommon.Map) {
	metric := dps.NumberDataPointSlice.At(i)
	if metric.Flags().NoRecordedValue() {
		return true, metric.Attributes()
	}
	if metric.ValueType() == pmetric.NumberDataPointValueTypeDouble {
		return math.IsNaN(metric.DoubleValue()) || math.IsInf(metric.DoubleValue(), 0), metric.Attributes()
	}
	return false, pcommon.Map{}
}

// CalculateDeltaDatapoints retrieves the HistogramDataPoint at the given index.
func (dps histogramDataPointSlice) CalculateDeltaDatapoints(i int, _ string, _ bool, calculators *emfCalculators) ([]dataPoint, bool) {
	metric := dps.HistogramDataPointSlice.At(i)
	labels := createLabels(metric.Attributes())
	timestamp := unixNanoToMilliseconds(metric.Timestamp())

	sum := metric.Sum()
	count := metric.Count()

	var datapoints []dataPoint

	if dps.adjustToDelta {
		var delta any
		mKey := aws.NewKey(dps.deltaMetricMetadata, labels)
		delta, retained := calculators.summary.Calculate(mKey, summaryMetricEntry{sum, count}, metric.Timestamp().AsTime())

		// If a delta to the previous data point could not be computed use the current metric value instead
		if !retained && dps.retainInitialValueForDelta {
			retained = true
			delta = summaryMetricEntry{sum, count}
		}

		if !retained {
			return datapoints, retained
		}
		summaryMetricDelta := delta.(summaryMetricEntry)
		sum = summaryMetricDelta.sum
		count = summaryMetricDelta.count
	}

	return []dataPoint{{
		name: dps.metricName,
		value: &cWMetricStats{
			Count: count,
			Sum:   sum,
			Max:   metric.Max(),
			Min:   metric.Min(),
		},
		labels:      labels,
		timestampMs: timestamp,
	}}, true
}

func (dps histogramDataPointSlice) IsStaleNaNInf(i int) (bool, pcommon.Map) {
	metric := dps.HistogramDataPointSlice.At(i)
	if metric.Flags().NoRecordedValue() {
		return true, metric.Attributes()
	}
	if math.IsNaN(metric.Max()) || math.IsNaN(metric.Sum()) ||
		math.IsNaN(metric.Min()) || math.IsInf(metric.Max(), 0) ||
		math.IsInf(metric.Sum(), 0) || math.IsInf(metric.Min(), 0) {
		return true, metric.Attributes()
	}
	return false, pcommon.Map{}
}

// CalculateDeltaDatapoints retrieves the ExponentialHistogramDataPoint at the given index.
// As CloudWatch EMF logs allows in maximum of 100 target members, the exponential histogram metric are split into two data points if the total buckets exceed 100,
// the first data point contains the first 100 buckets, while the second data point includes the remaining buckets.
// Re-calculated Min, Max, Sum, Count for each split:
// 1. First split datapoint:
//   - Max: From original metric.
//   - Min: Last bucket’s bucketBegin in the first split.
//   - Sum: From original metric.
//   - Count: Calculated from the first split buckets.
//
// 2. Second split datapoint:
//   - Max: First bucket’s bucketEnd in the second split.
//   - Min: From original metric.
//   - Sum: 0.
//   - Count: Overall count - first split count.
func (dps exponentialHistogramDataPointSlice) CalculateDeltaDatapoints(idx int, _ string, _ bool, _ *emfCalculators) ([]dataPoint, bool) {
	metric := dps.ExponentialHistogramDataPointSlice.At(idx)
	scale := metric.Scale()
	base := math.Pow(2, math.Pow(2, float64(-scale)))

	var bucketBegin float64
	var bucketEnd float64
	totalBucketLen := metric.Positive().BucketCounts().Len() + metric.Negative().BucketCounts().Len()
	if metric.ZeroCount() > 0 {
		totalBucketLen++
	}
	firstSplit := dataPointSplit{
		values:     []float64{},
		counts:     []float64{},
		count:      int(metric.Count()),
		length:     0,
		bucketMin:  metric.Min(),
		bucketMax:  metric.Max(),
		maxBuckets: totalBucketLen,
	}

	currentLength := 0
	splitThreshold := 100
	var secondSplit dataPointSplit
	if totalBucketLen > splitThreshold {
		firstSplit.maxBuckets = splitThreshold
		firstSplit.count = 0
		secondSplit = dataPointSplit{
			values:     []float64{},
			counts:     []float64{},
			count:      0,
			length:     0,
			bucketMin:  metric.Min(),
			bucketMax:  metric.Max(),
			maxBuckets: totalBucketLen - splitThreshold,
		}
	}

	// Set mid-point of positive buckets in values/counts array.
	positiveBuckets := metric.Positive()
	positiveOffset := positiveBuckets.Offset()
	positiveBucketCounts := positiveBuckets.BucketCounts()
	bucketBegin = 0
	bucketEnd = 0
	for i := positiveBucketCounts.Len() - 1; i >= 0; i-- {
		index := i + int(positiveOffset)
		if bucketEnd == 0 {
			bucketEnd = math.Pow(base, float64(index+1))
		} else {
			bucketEnd = bucketBegin
		}
		bucketBegin = math.Pow(base, float64(index))
		metricVal := (bucketBegin + bucketEnd) / 2
		count := positiveBucketCounts.At(i)
		if count > 0 && firstSplit.length < firstSplit.maxBuckets {
			firstSplit.values = append(firstSplit.values, metricVal)
			firstSplit.counts = append(firstSplit.counts, float64(count))
			firstSplit.length++
			currentLength++
			if firstSplit.maxBuckets < totalBucketLen {
				firstSplit.count += int(count)
				if currentLength == firstSplit.maxBuckets {
					if bucketBegin < bucketEnd {
						firstSplit.bucketMin = bucketBegin
					} else {
						firstSplit.bucketMin = bucketEnd
					}
				}
			}
		} else if count > 0 {
			if currentLength == firstSplit.maxBuckets && currentLength != 0 {
				if bucketBegin < bucketEnd {
					secondSplit.bucketMax = bucketEnd
				} else {
					secondSplit.bucketMax = bucketBegin
				}
			}
			secondSplit.values = append(secondSplit.values, metricVal)
			secondSplit.counts = append(secondSplit.counts, float64(count))
			currentLength++
		}
	}

	// Set count of zero bucket in values/counts array.
	if metric.ZeroCount() > 0 && firstSplit.length < firstSplit.maxBuckets {
		firstSplit.values = append(firstSplit.values, 0)
		firstSplit.counts = append(firstSplit.counts, float64(metric.ZeroCount()))
		firstSplit.length++
		currentLength++
		if firstSplit.maxBuckets < totalBucketLen {
			firstSplit.count += int(metric.ZeroCount())
			if currentLength == firstSplit.maxBuckets {
				firstSplit.bucketMin = bucketBegin
			}
		}
	} else if metric.ZeroCount() > 0 {
		if currentLength == firstSplit.maxBuckets && currentLength != 0 {
			secondSplit.bucketMax = 0
		}
		secondSplit.values = append(secondSplit.values, 0)
		secondSplit.counts = append(secondSplit.counts, float64(metric.ZeroCount()))
		currentLength++
	}

	// Set mid-point of negative buckets in values/counts array.
	// According to metrics spec, the value in histogram is expected to be non-negative.
	// https://opentelemetry.io/docs/specs/otel/metrics/api/#histogram
	// However, the negative support is defined in metrics data model.
	// https://opentelemetry.io/docs/specs/otel/metrics/data-model/#exponentialhistogram
	// The negative is also supported but only verified with unit test.

	negativeBuckets := metric.Negative()
	negativeOffset := negativeBuckets.Offset()
	negativeBucketCounts := negativeBuckets.BucketCounts()
	bucketBegin = 0
	bucketEnd = 0
	for i := 0; i < negativeBucketCounts.Len(); i++ {
		index := i + int(negativeOffset)
		if bucketEnd == 0 {
			bucketEnd = -math.Pow(base, float64(index))
		} else {
			bucketEnd = bucketBegin
		}
		bucketBegin = -math.Pow(base, float64(index+1))
		metricVal := (bucketBegin + bucketEnd) / 2
		count := negativeBucketCounts.At(i)
		if count > 0 && firstSplit.length < firstSplit.maxBuckets {
			firstSplit.values = append(firstSplit.values, metricVal)
			firstSplit.counts = append(firstSplit.counts, float64(count))
			firstSplit.length++
			currentLength++
			if firstSplit.maxBuckets < totalBucketLen {
				firstSplit.count += int(count)
				if currentLength == firstSplit.length {
					firstSplit.bucketMin = bucketEnd
					if bucketBegin < bucketEnd {
						firstSplit.bucketMin = bucketBegin
					} else {
						firstSplit.bucketMin = bucketEnd
					}
				}
			}
		} else if count > 0 {
			if currentLength == firstSplit.maxBuckets && currentLength != 0 {
				if bucketBegin < bucketEnd {
					secondSplit.bucketMax = bucketEnd
				} else {
					secondSplit.bucketMax = bucketBegin
				}
			}
			secondSplit.values = append(secondSplit.values, metricVal)
			secondSplit.counts = append(secondSplit.counts, float64(count))
			currentLength++
		}
	}

	//fmt.Println("firstSplit", firstSplit)
	//fmt.Println("secondSplit", secondSplit)

	var datapoints []dataPoint
	// Add second data point (last 100 elements or fewer)
	datapoints = append(datapoints, dataPoint{
		name: dps.metricName,
		value: &cWMetricHistogram{
			Values: firstSplit.values,
			Counts: firstSplit.counts,
			Count:  uint64(firstSplit.count),
			Sum:    metric.Sum(),
			Max:    firstSplit.bucketMax,
			Min:    firstSplit.bucketMin,
		},
		labels:      createLabels(metric.Attributes()),
		timestampMs: unixNanoToMilliseconds(metric.Timestamp()),
	})

	// Add first data point with the remaining elements
	if totalBucketLen > 100 {
		datapoints = append(datapoints, dataPoint{
			name: dps.metricName,
			value: &cWMetricHistogram{
				Values: secondSplit.values,
				Counts: secondSplit.counts,
				Count:  metric.Count() - uint64(firstSplit.count),
				Sum:    0,
				Max:    secondSplit.bucketMax,
				Min:    secondSplit.bucketMin,
			},
			labels:      createLabels(metric.Attributes()),
			timestampMs: unixNanoToMilliseconds(metric.Timestamp()),
		})
	}
	return datapoints, true
}

func (dps exponentialHistogramDataPointSlice) IsStaleNaNInf(i int) (bool, pcommon.Map) {
	metric := dps.ExponentialHistogramDataPointSlice.At(i)
	if metric.Flags().NoRecordedValue() {
		return true, metric.Attributes()
	}
	if math.IsNaN(metric.Max()) ||
		math.IsNaN(metric.Min()) ||
		math.IsNaN(metric.Sum()) ||
		math.IsInf(metric.Max(), 0) ||
		math.IsInf(metric.Min(), 0) ||
		math.IsInf(metric.Sum(), 0) {
		return true, metric.Attributes()
	}

	return false, pcommon.Map{}
}

// CalculateDeltaDatapoints retrieves the SummaryDataPoint at the given index and perform calculation with sum and count while retain the quantile value.
func (dps summaryDataPointSlice) CalculateDeltaDatapoints(i int, _ string, detailedMetrics bool, calculators *emfCalculators) ([]dataPoint, bool) {
	metric := dps.SummaryDataPointSlice.At(i)
	labels := createLabels(metric.Attributes())
	timestampMs := unixNanoToMilliseconds(metric.Timestamp())

	sum := metric.Sum()
	count := metric.Count()

	retained := true
	datapoints := []dataPoint{}

	if dps.adjustToDelta {
		var delta any
		mKey := aws.NewKey(dps.deltaMetricMetadata, labels)
		delta, retained = calculators.summary.Calculate(mKey, summaryMetricEntry{sum, count}, metric.Timestamp().AsTime())

		// If a delta to the previous data point could not be computed use the current metric value instead
		if !retained && dps.retainInitialValueForDelta {
			retained = true
			delta = summaryMetricEntry{sum, count}
		}

		if !retained {
			return datapoints, retained
		}
		summaryMetricDelta := delta.(summaryMetricEntry)
		sum = summaryMetricDelta.sum
		count = summaryMetricDelta.count
	}

	if detailedMetrics {
		// Instead of sending metrics as a Statistical Set (contains min,max, count, sum), the emfexporter will enrich the
		// values by sending each quantile values as a datapoint (from quantile 0 ... 1)
		values := metric.QuantileValues()
		datapoints = append(datapoints, dataPoint{name: fmt.Sprint(dps.metricName, summarySumSuffix), value: sum, labels: labels, timestampMs: timestampMs})
		datapoints = append(datapoints, dataPoint{name: fmt.Sprint(dps.metricName, summaryCountSuffix), value: count, labels: labels, timestampMs: timestampMs})

		for i := 0; i < values.Len(); i++ {
			cLabels := maps.Clone(labels)
			quantile := values.At(i)
			cLabels["quantile"] = strconv.FormatFloat(quantile.Quantile(), 'g', -1, 64)
			datapoints = append(datapoints, dataPoint{name: dps.metricName, value: quantile.Value(), labels: cLabels, timestampMs: timestampMs})

		}
	} else {
		metricVal := &cWMetricStats{Count: count, Sum: sum}
		if quantileValues := metric.QuantileValues(); quantileValues.Len() > 0 {
			metricVal.Min = quantileValues.At(0).Value()
			metricVal.Max = quantileValues.At(quantileValues.Len() - 1).Value()
		}
		datapoints = append(datapoints, dataPoint{name: dps.metricName, value: metricVal, labels: labels, timestampMs: timestampMs})
	}

	return datapoints, retained
}

func (dps summaryDataPointSlice) IsStaleNaNInf(i int) (bool, pcommon.Map) {
	metric := dps.SummaryDataPointSlice.At(i)
	if metric.Flags().NoRecordedValue() {
		return true, metric.Attributes()
	}
	if math.IsNaN(metric.Sum()) || math.IsInf(metric.Sum(), 0) {
		return true, metric.Attributes()
	}

	values := metric.QuantileValues()
	for i := 0; i < values.Len(); i++ {
		quantile := values.At(i)
		if math.IsNaN(quantile.Value()) || math.IsNaN(quantile.Quantile()) ||
			math.IsInf(quantile.Value(), 0) || math.IsInf(quantile.Quantile(), 0) {
			return true, metric.Attributes()
		}
	}

	return false, metric.Attributes()
}

// createLabels converts OTel AttributesMap attributes to a map
// and optionally adds in the OTel instrumentation library name
func createLabels(attributes pcommon.Map) map[string]string {
	labels := make(map[string]string, attributes.Len()+1)
	attributes.Range(func(k string, v pcommon.Value) bool {
		labels[k] = v.AsString()
		return true
	})

	return labels
}

// getDataPoints retrieves data points from OT Metric.
func getDataPoints(pmd pmetric.Metric, metadata cWMetricMetadata, logger *zap.Logger) dataPoints {
	metricMetadata := deltaMetricMetadata{
		adjustToDelta:              false,
		retainInitialValueForDelta: metadata.retainInitialValueForDelta,
		metricName:                 pmd.Name(),
		namespace:                  metadata.namespace,
		logGroup:                   metadata.logGroup,
		logStream:                  metadata.logStream,
	}

	var dps dataPoints

	//exhaustive:enforce
	switch pmd.Type() {
	case pmetric.MetricTypeGauge:
		metric := pmd.Gauge()
		dps = numberDataPointSlice{
			metricMetadata,
			metric.DataPoints(),
		}
	case pmetric.MetricTypeSum:
		metric := pmd.Sum()
		metricMetadata.adjustToDelta = metric.AggregationTemporality() == pmetric.AggregationTemporalityCumulative
		dps = numberDataPointSlice{
			metricMetadata,
			metric.DataPoints(),
		}
	case pmetric.MetricTypeHistogram:
		metric := pmd.Histogram()
		// the prometheus histograms from the container insights should be adjusted for delta
		metricMetadata.adjustToDelta = metadata.receiver == containerInsightsReceiver
		dps = histogramDataPointSlice{
			metricMetadata,
			metric.DataPoints(),
		}
	case pmetric.MetricTypeExponentialHistogram:
		metric := pmd.ExponentialHistogram()
		dps = exponentialHistogramDataPointSlice{
			metricMetadata,
			metric.DataPoints(),
		}
	case pmetric.MetricTypeSummary:
		metric := pmd.Summary()
		// For summaries coming from the prometheus receiver, the sum and count are cumulative, whereas for summaries
		// coming from other sources, e.g. SDK, the sum and count are delta by being accumulated and reset periodically.
		// In order to ensure metrics are sent as deltas, we check the receiver attribute (which can be injected by
		// attribute processor) from resource metrics. If it exists, and equals to prometheus, the sum and count will be
		// converted.
		// For more information: https://github.com/open-telemetry/opentelemetry-collector/blob/main/receiver/prometheusreceiver/DESIGN.md#summary
		metricMetadata.adjustToDelta = metadata.receiver == prometheusReceiver || metadata.receiver == containerInsightsReceiver
		dps = summaryDataPointSlice{
			metricMetadata,
			metric.DataPoints(),
		}
	default:
		logger.Warn("Unhandled metric data type.",
			zap.String("DataType", pmd.Type().String()),
			zap.String("Name", pmd.Name()),
			zap.String("Unit", pmd.Unit()),
		)
	}

	return dps
}
