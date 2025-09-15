// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package generator_test

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/generator"
)

func ExampleHistogramGenerator_GenerateHistogram() {
	// Create a generator with a fixed seed for reproducible results
	gen := generator.NewHistogramGenerator(generator.GenerationOptions{
		Seed: 12345,
	})

	// Define histogram input parameters
	input := generator.HistogramInput{
		Count:      1000,
		Min:        ptr(10.0),
		Max:        ptr(200.0),
		Boundaries: []float64{25, 50, 75, 100, 150},
		Attributes: map[string]string{
			"service.name": "payment-service",
			"environment":  "production",
		},
	}

	// Generate histogram using normal distribution
	result, err := gen.GenerateHistogram(input, func(rnd *rand.Rand, t time.Time) float64 {
		return generator.NormalRandom(rnd, 75, 25) // mean=75, stddev=25
	})

	if err != nil {
		panic(err)
	}

	fmt.Printf("Generated %d samples with sum=%.2f, avg=%.2f\n",
		result.Expected.Count, result.Expected.Sum, result.Expected.Average)
	fmt.Printf("Min=%.2f, Max=%.2f\n", *result.Expected.Min, *result.Expected.Max)

	// Output will vary due to randomness, but structure is consistent
}

func ExampleHistogramGenerator_GenerateAndPublishHistograms() {
	// Create a generator with OTLP endpoint
	gen := generator.NewHistogramGenerator(generator.GenerationOptions{
		Seed:     time.Now().UnixNano(),
		Endpoint: "localhost:4318", // OTLP HTTP endpoint
	})

	input := generator.HistogramInput{
		Count:      500,
		Boundaries: []float64{10, 50, 100, 500, 1000},
		Attributes: map[string]string{
			"service.name":    "web-service",
			"service.version": "1.0.0",
			"environment":     "staging",
		},
	}

	// Generate and publish using exponential distribution
	result, err := gen.GenerateAndPublishHistograms(input, func(rnd *rand.Rand, t time.Time) float64 {
		return generator.ExponentialRandom(rnd, 0.01) // rate=0.01
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Generated and published histogram with %d samples\n", result.Expected.Count)
}

func ExampleOTLPPublisher_SendMetrics() {
	// Example of using telemetrygen for different metric types
	publisher := generator.NewOTLPPublisher("localhost:4318")

	// Send different types of metrics using telemetrygen
	err := publisher.SendSumMetric("requests_total", 100)
	if err != nil {
		fmt.Printf("Error sending sum metric: %v\n", err)
		return
	}

	err = publisher.SendGaugeMetric("cpu_usage", 75.5)
	if err != nil {
		fmt.Printf("Error sending gauge metric: %v\n", err)
		return
	}

	err = publisher.SendHistogramMetricSimple("response_time")
	if err != nil {
		fmt.Printf("Error sending histogram metric: %v\n", err)
		return
	}

	fmt.Println("All metrics sent successfully using telemetrygen")
}

// Helper function to create float64 pointers
func ptr(f float64) *float64 {
	return &f
}
