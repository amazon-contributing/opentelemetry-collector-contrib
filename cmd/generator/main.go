package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/generator/generator"
	types "github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen/pkg"
	"github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen/pkg/metrics"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	defaultGRPCEndpoint = "localhost:4317"
	defaultHTTPEndpoint = "localhost:4318"
)

var (
	errFormatOTLPAttributes       = errors.New("value should be in one of the following formats: key=\"value\", key=true, key=false, or key=<integer>")
	errDoubleQuotesOTLPAttributes = errors.New("value should be a string wrapped in double quotes")
)

type KeyValue map[string]any

var _ pflag.Value = (*KeyValue)(nil)

func (*KeyValue) String() string {
	return ""
}

func (v *KeyValue) Set(s string) error {
	kv := strings.SplitN(s, "=", 2)
	if len(kv) != 2 {
		return errFormatOTLPAttributes
	}
	val := kv[1]
	if val == "true" {
		(*v)[kv[0]] = true
		return nil
	}
	if val == "false" {
		(*v)[kv[0]] = false
		return nil
	}
	if intVal, err := strconv.Atoi(val); err == nil {
		(*v)[kv[0]] = intVal
		return nil
	}
	if len(val) < 2 || !strings.HasPrefix(val, "\"") || !strings.HasSuffix(val, "\"") {
		return errDoubleQuotesOTLPAttributes
	}

	(*v)[kv[0]] = val[1 : len(val)-1]
	return nil
}

func (*KeyValue) Type() string {
	return "map[string]any"
}

type ClientAuth struct {
	Enabled        bool
	ClientCertFile string
	ClientKeyFile  string
}

// Config describes the test scenario.
type Config struct {
	WorkerCount           int
	Rate                  float64
	TotalDuration         types.DurationWithInf
	ReportingInterval     time.Duration
	SkipSettingGRPCLogger bool

	// OTLP config
	CustomEndpoint      string
	Insecure            bool
	InsecureSkipVerify  bool
	UseHTTP             bool
	HTTPPath            string
	Headers             KeyValue
	ResourceAttributes  KeyValue
	ServiceName         string
	TelemetryAttributes KeyValue

	// OTLP TLS configuration
	CaFile string

	// OTLP mTLS configuration
	ClientAuth ClientAuth

	// Export behavior configuration
	AllowExportFailures bool

	// Load testing configuration
	LoadSize int

	NumMetrics              int
	MetricName              string
	MetricType              metrics.MetricType
	AggregationTemporality  metricdata.Temporality
	SpanID                  string
	TraceID                 string
	EnforceUniqueTimeseries bool
	UniqueTimelimit         time.Duration
}

// Endpoint returns the appropriate endpoint URL based on the selected communication mode (gRPC or HTTP)
// or custom endpoint provided in the configuration.
func (c *Config) Endpoint() string {
	if c.CustomEndpoint != "" {
		return c.CustomEndpoint
	}
	if c.UseHTTP {
		return defaultHTTPEndpoint
	}
	return defaultGRPCEndpoint
}

func (c *Config) GetHeaders() map[string]string {
	m := make(map[string]string, len(c.Headers))

	for k, t := range c.Headers {
		switch v := t.(type) {
		case bool:
			m[k] = strconv.FormatBool(v)
		case string:
			m[k] = v
		}
	}

	return m
}

func main() {

	gen := generator.NewHistogramGenerator(generator.GenerationOptions{
		Seed: 12345, // For reproducible results
	})

	input := generator.HistogramInput{
		Count:      1000,
		Min:        ptr(0.0),
		Max:        ptr(200.0),
		Boundaries: []float64{25, 50, 75, 100, 150},
		Attributes: map[string]string{"service.name": "test-service"},
	}

	result, err := gen.GenerateHistogram(input, func(rnd *rand.Rand, t time.Time) float64 {
		return generator.NormalRandom(rnd, 75, 25) // mean=75, stddev=25
	})

	exporter, err := createExporter(&Config{
		UseHTTP:  true,
		Insecure: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	res := resource.NewWithAttributes(semconv.SchemaURL)
	limiter := rate.NewLimiter(1, 1)

	startTime := time.Now()

	for {
		if err := limiter.Wait(context.Background()); err != nil {
			log.Printf("limiter wait failed, retry", zap.Error(err))
		}

		attrs := []attribute.KeyValue{}
		for k, v := range result.Input.Attributes {
			attrs = append(attrs, attribute.String(k, v))
		}
		metrics := []metricdata.Metrics{metricdata.Metrics{
			Name: "CustomOTLPHistogram",
			Data: metricdata.Histogram[float64]{
				Temporality: metricdata.DeltaTemporality,
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						StartTime:    startTime,
						Time:         time.Now(),
						Attributes:   attribute.NewSet(attrs...),
						Count:        result.Input.Count,
						Sum:          result.Input.Sum,
						Min:          metricdata.NewExtrema[float64](*result.Input.Min),
						Max:          metricdata.NewExtrema[float64](*result.Input.Max),
						Bounds:       result.Input.Boundaries,
						BucketCounts: result.Input.Counts,
					},
				},
			},
		}}
		rm := metricdata.ResourceMetrics{
			Resource:     res,
			ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: metrics}},
		}

		if err := exporter.Export(context.Background(), &rm); err != nil {
			log.Fatal("exporter failed", zap.Error(err))
		}
	}

}

func createExporter(cfg *Config) (sdkmetric.Exporter, error) {
	var exp sdkmetric.Exporter
	var err error
	if cfg.UseHTTP {
		var exporterOpts []otlpmetrichttp.Option

		log.Print("starting HTTP exporter")
		exporterOpts, err = httpExporterOptions(cfg)
		if err != nil {
			return nil, err
		}
		exp, err = otlpmetrichttp.New(context.Background(), exporterOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to obtain OTLP HTTP exporter: %w", err)
		}
	} else {
		return nil, fmt.Errorf("NotYetImplemented")
	}
	return exp, err
}

// httpExporterOptions creates the configuration options for an HTTP-based OTLP metric exporter.
// It configures the exporter with the provided endpoint, URL path, connection security settings, and headers.
func httpExporterOptions(cfg *Config) ([]otlpmetrichttp.Option, error) {
	httpExpOpt := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(cfg.Endpoint()),
		otlpmetrichttp.WithURLPath(cfg.HTTPPath),
	}

	if cfg.Insecure {
		httpExpOpt = append(httpExpOpt, otlpmetrichttp.WithInsecure())
	}

	if len(cfg.Headers) > 0 {
		httpExpOpt = append(httpExpOpt, otlpmetrichttp.WithHeaders(cfg.GetHeaders()))
	}

	return httpExpOpt, nil
}

func ptr(f float64) *float64 {
	return &f
}
