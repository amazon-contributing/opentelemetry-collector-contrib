// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package histograms // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/aws/cloudwatch/histograms"

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/aws/cloudwatch"
)

var filenameReplacer = strings.NewReplacer(
	" ", "_",
	"/", "_",
)

func TestWriteInputHistograms(t *testing.T) {
	t.Skip("only used to create test data for visualization")
	for _, tc := range TestCases() {
		jsonData, err := json.MarshalIndent(tc.Input, "", "  ")
		require.NoError(t, err)
		_ = os.Mkdir("testdata/input", os.ModePerm)
		require.NoError(t, os.WriteFile("testdata/input/"+filenameReplacer.Replace(tc.Name)+".json", jsonData, 0o600))
	}
}

func TestWriteConvertedHistograms(t *testing.T) {
	t.Skip("only used to create test data for visualization")
	for _, tc := range TestCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dp := setupDatapoint(tc.Input)
			dist := ConvertOTelToCloudWatch(dp)
			_ = os.Mkdir("testdata/exponential", os.ModePerm)
			assert.NoError(t, writeValuesAndCountsToJSON(dist, "testdata/exponential/"+filenameReplacer.Replace(tc.Name+".json")))
		})
	}
}

func TestConvertOTelToCloudWatch(t *testing.T) {
	for _, tc := range TestCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dp := setupDatapoint(tc.Input)
			dist := ConvertOTelToCloudWatch(dp)
			verifyDist(t, dist, tc.Expected)
		})
	}

	t.Run("accuracy test - lognormal", func(t *testing.T) {
		verifyDistAccuracy(t, ConvertOTelToCloudWatch, "testdata/lognormal_10000.csv")
	})

	t.Run("accuracy test - weibull", func(t *testing.T) {
		verifyDistAccuracy(t, ConvertOTelToCloudWatch, "testdata/weibull_10000.csv")
	})
}

func BenchmarkLogNormal(b *testing.B) {
	// arrange
	boundaries := []float64{
		0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.007, 0.008, 0.009, 0.01,
		0.011, 0.012, 0.013, 0.014, 0.015, 0.016, 0.017, 0.018, 0.019, 0.02,
		0.021, 0.022, 0.023, 0.024, 0.025, 0.026, 0.027, 0.028, 0.029, 0.03,
		0.031, 0.032, 0.033, 0.034, 0.035, 0.036, 0.037, 0.038, 0.039, 0.04,
		0.041, 0.042, 0.043, 0.044, 0.045, 0.046, 0.047, 0.048, 0.049, 0.05,
		0.1, 0.2,
	}

	data, err := loadCsvData("testdata/lognormal_10000.csv")
	require.NoError(b, err)
	require.Len(b, data, 10000)

	dp := createHistogramDatapointFromData(data, boundaries)
	require.Equal(b, 10000, int(dp.Count()))

	b.Run("NewExponentialMappingCWFromOtel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dist := ConvertOTelToCloudWatch(dp)
			values, counts := dist.ValuesAndCounts()
			assert.NotNil(b, values)
			assert.NotNil(b, counts)
		}
	})
}

func BenchmarkWeibull(b *testing.B) {
	// arrange
	boundaries := []float64{
		0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.007, 0.008, 0.009, 0.01,
		0.011, 0.012, 0.013, 0.014, 0.015, 0.016, 0.017, 0.018, 0.019, 0.02,
		0.021, 0.022, 0.023, 0.024, 0.025, 0.026, 0.027, 0.028, 0.029, 0.03,
		0.031, 0.032, 0.033, 0.034, 0.035, 0.036, 0.037, 0.038, 0.039, 0.04,
		0.041, 0.042, 0.043, 0.044, 0.045, 0.046, 0.047, 0.048, 0.049, 0.05,
		0.1, 0.2,
	}

	data, err := loadCsvData("testdata/weibull_10000.csv")
	require.NoError(b, err)
	require.Len(b, data, 10000)

	dp := createHistogramDatapointFromData(data, boundaries)
	require.Equal(b, 10000, int(dp.Count()))

	b.Run("NewExponentialMappingCWFromOtel", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dist := ConvertOTelToCloudWatch(dp)
			values, counts := dist.ValuesAndCounts()
			assert.NotNil(b, values)
			assert.NotNil(b, counts)
		}
	})
}

func setupDatapoint(input HistogramInput) pmetric.HistogramDataPoint {
	dp := pmetric.NewHistogramDataPoint()
	dp.SetCount(input.Count)
	dp.SetSum(input.Sum)
	if input.Min != nil {
		dp.SetMin(*input.Min)
	}
	if input.Max != nil {
		dp.SetMax(*input.Max)
	}
	dp.ExplicitBounds().FromRaw(input.Boundaries)
	dp.BucketCounts().FromRaw(input.Counts)
	return dp
}

func verifyDist(t *testing.T, dist cloudwatch.HistogramDataPoint, expected ExpectedMetrics) {
	if expected.Min != nil {
		assert.Equal(t, *expected.Min, dist.Minimum(), "min does not match expected")
	}
	if expected.Max != nil {
		assert.Equal(t, *expected.Max, dist.Maximum(), "max does not match expected")
	}
	assert.Equal(t, int(expected.Count), int(dist.SampleCount()), "samplecount does not match expected")
	assert.Equal(t, expected.Sum, dist.Sum(), "sum does not match expected")

	values, counts := dist.ValuesAndCounts()

	calculatedCount := 0.0
	for _, count := range counts {
		calculatedCount += count
		// fmt.Printf("%7.2f = %4d (%d)\n", values[i], int(counts[i]), calculatedCount)
	}
	assert.InDelta(t, float64(expected.Count), calculatedCount, 1e-6, "calculated count does not match expected")

	for p, r := range expected.PercentileRanges {
		x := int(math.Round(float64(dist.SampleCount()) * p))

		soFar := 0
		for i, count := range counts {
			soFar += int(count)
			if soFar >= x {
				// fmt.Printf("Found p%.f at bucket %0.2f. Expected range: %+v\n", p*100, values[i], r)
				assert.GreaterOrEqual(t, values[i], r.Low, "percentile %0.2f", p)
				assert.LessOrEqual(t, values[i], r.High, "percentile %0.2f", p)
				break
			}
		}
	}
}

func loadCsvData(filename string) ([]float64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var data []float64
	for _, value := range records[0] {
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, err
		}
		data = append(data, f)
	}
	return data, nil
}

func createHistogramDatapointFromData(data []float64, boundaries []float64) pmetric.HistogramDataPoint {
	dp := pmetric.NewHistogramDataPoint()

	// Calculate basic stats
	var sum float64
	minimum := math.Inf(1)
	maximum := math.Inf(-1)

	for _, v := range data {
		sum += v
		if v < minimum {
			minimum = v
		}
		if v > maximum {
			maximum = v
		}
	}

	dp.SetCount(uint64(len(data)))
	dp.SetSum(sum)
	dp.SetMin(minimum)
	dp.SetMax(maximum)

	// Create bucket counts
	bucketCounts := make([]uint64, len(boundaries)+1)

	for _, v := range data {
		bucket := sort.SearchFloat64s(boundaries, v)
		bucketCounts[bucket]++
	}

	dp.ExplicitBounds().FromRaw(boundaries)
	dp.BucketCounts().FromRaw(bucketCounts)

	return dp
}

func verifyDistAccuracy(t *testing.T, newDistFunc func(pmetric.HistogramDataPoint) cloudwatch.HistogramDataPoint, filename string) {
	// arrange
	percentiles := []float64{0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 0.999}
	boundaries := []float64{
		0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.007, 0.008, 0.009, 0.01,
		0.011, 0.012, 0.013, 0.014, 0.015, 0.016, 0.017, 0.018, 0.019, 0.02,
		0.021, 0.022, 0.023, 0.024, 0.025, 0.026, 0.027, 0.028, 0.029, 0.03,
		0.031, 0.032, 0.033, 0.034, 0.035, 0.036, 0.037, 0.038, 0.039, 0.04,
		0.041, 0.042, 0.043, 0.044, 0.045, 0.046, 0.047, 0.048, 0.049, 0.05,
		0.1, 0.2,
	}

	data, err := loadCsvData(filename)
	require.NoError(t, err)
	assert.Len(t, data, 10000)

	dp := createHistogramDatapointFromData(data, boundaries)
	assert.Equal(t, 10000, int(dp.Count()))
	calculatedTotal := 0
	for _, count := range dp.BucketCounts().All() {
		calculatedTotal += int(count)
	}
	assert.Equal(t, 10000, calculatedTotal)

	// act
	dist := newDistFunc(dp)
	values, counts := dist.ValuesAndCounts()

	// assert
	calculatedCount := 0.0
	for _, count := range counts {
		calculatedCount += count
	}
	assert.InDelta(t, 10000, calculatedCount, 1e-6, "calculated count does not match expected")

	for _, p := range percentiles {
		x1 := int(math.Round(float64(dp.Count()) * p))
		x2 := int(math.Round(calculatedCount * p))

		exactPercentileValue := data[x1]

		soFar := 0
		for i, count := range counts {
			soFar += int(count)
			if soFar >= x2 {
				calculatedPercentileValue := values[i]
				errorPercent := (exactPercentileValue - calculatedPercentileValue) / exactPercentileValue * 100
				fmt.Printf("P%.1f: exact=%.6f, calculated=%.6f, error=%.2f%%\n", p*100, exactPercentileValue, calculatedPercentileValue, errorPercent)
				break
			}
		}
	}
}

func writeValuesAndCountsToJSON(dist cloudwatch.HistogramDataPoint, filename string) error {
	values, counts := dist.ValuesAndCounts()

	data := make(map[string]any)
	data["values"] = values
	data["counts"] = counts
	data["sum"] = dist.Sum()

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, jsonData, 0o600)
}
