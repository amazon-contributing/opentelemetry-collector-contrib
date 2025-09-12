package main

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen/pkg/metrics"
)

// MetricType represents supported OpenTelemetry metric types
type MetricType string

const (
	TypeGauge     MetricType = "gauge"
	TypeSum       MetricType = "sum"
	TypeHistogram MetricType = "histogram"
)

// GenerateMetrics creates and generates OpenTelemetry metrics based on the specified type
func GenerateMetrics(cfg *metrics.Config, metricType MetricType, name string, value float64) error {
	switch metricType {
	case TypeSum:
		cfg.MetricType = metrics.MetricTypeSum
	case TypeGauge:
		cfg.MetricType = metrics.MetricTypeGauge
	case TypeHistogram:
		cfg.MetricType = metrics.MetricTypeHistogram
	default:
		cfg.MetricType = metrics.MetricTypeGauge // default to gauge
	}

	cfg.MetricName = name
	cfg.NumMetrics = 1
	cfg.Rate = 1

	return metrics.Start(cfg)
}

func main() {
	// Set up endpoint configuration
	cfg := metrics.NewConfig()
	cfg.CustomEndpoint = "localhost:4318" // OTLP HTTP endpoint (host:port only)
	cfg.UseHTTP = true                    // Use HTTP instead of gRPC
	cfg.Insecure = true                   // Use HTTP instead of HTTPS
	// HTTPPath is already set to "/v1/metrics" by default

	// Generate different types of metrics and publish to endpoint
	err := GenerateMetrics(cfg, TypeSum, "requests_total", 100)
	if err != nil {
		panic(err)
	}

	err = GenerateMetrics(cfg, TypeGauge, "cpu_usage", 75.5)
	if err != nil {
		panic(err)
	}

	err = GenerateMetrics(cfg, TypeHistogram, "response_time", 250.0)
	if err != nil {
		panic(err)
	}
}
