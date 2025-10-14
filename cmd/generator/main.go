package main

import (
	"log"
	"math/rand"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/generator/generator"
)

func main() {
	gen := generator.NewHistogramGenerator(generator.GenerationOptions{
		Seed:     time.Now().UnixNano(),
		Endpoint: "localhost:4318", // OTLP HTTP endpoint
	})

	ticker := time.NewTicker(time.Second * 10)
	for range ticker.C {
		_, err := gen.GenerateAndPublishHistograms(
			generator.HistogramInput{
				Min:   ptr(0),
				Max:   ptr(100),
				Count: 1000,
				Boundaries: []float64{
					0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100,
				},
				Attributes: map[string]string{
					"service.name": "test-service",
					"operation":    "test-operation",
				},
			}, func(rand *rand.Rand, time time.Time) float64 {
				return generator.SinusoidalValue(rand, time, 100, 0.1, 0, 1)
			})
		if err != nil {
			log.Printf("Error generating histograms: %v\n", err)
		}
	}

}

func ptr(f float64) *float64 {
	return &f
}
