package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen/pkg/metrics"
)

// MetricType represents supported OpenTelemetry metric types
type MetricType string

const (
	TypeGauge     MetricType = "gauge"
	TypeSum       MetricType = "sum"
	TypeHistogram MetricType = "histogram"
)

// MetricDefinition defines a metric and its generation properties
type MetricDefinition struct {
	Name        string
	Type        MetricType
	Description string
	Labels      map[string]string
	ValueFunc   func(*rand.Rand, time.Time) float64
}

// Generator manages advanced metric generation using telemetrygen
type Generator struct {
	config      *metrics.Config
	definitions []MetricDefinition
	rand        *rand.Rand
	mu          sync.RWMutex
}

// NewGenerator creates a new metrics generator
func NewGenerator(endpoint string) *Generator {
	cfg := metrics.NewConfig()
	cfg.CustomEndpoint = endpoint
	cfg.UseHTTP = true
	cfg.Insecure = true
	cfg.NumMetrics = 1
	cfg.Rate = 1

	return &Generator{
		config:      cfg,
		definitions: make([]MetricDefinition, 0),
		rand:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// AddMetric adds a new metric definition to the generator
func (g *Generator) AddMetric(def MetricDefinition) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.definitions = append(g.definitions, def)
}

// GenerateMetrics generates all defined metrics
func (g *Generator) GenerateMetrics() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	timestamp := time.Now()

	for _, def := range g.definitions {
		// Create a copy of config for each metric
		cfg := *g.config

		// Set metric type
		switch def.Type {
		case TypeSum:
			cfg.MetricType = metrics.MetricTypeSum
		case TypeGauge:
			cfg.MetricType = metrics.MetricTypeGauge
		case TypeHistogram:
			cfg.MetricType = metrics.MetricTypeHistogram
		default:
			cfg.MetricType = metrics.MetricTypeGauge
		}

		cfg.MetricName = def.Name

		// Generate value using the value function
		var value float64
		if def.ValueFunc != nil {
			value = def.ValueFunc(g.rand, timestamp)
		} else {
			value = g.rand.Float64() * 100 // default random value
		}

		log.Printf("Generating metric: %s = %f (type: %s)", def.Name, value, def.Type)

		// Generate the metric using telemetrygen
		if err := metrics.Start(&cfg); err != nil {
			return fmt.Errorf("failed to generate metric %s: %w", def.Name, err)
		}
	}

	return nil
}

// GammaRandom generates a random number from a Gamma distribution
func GammaRandom(rnd *rand.Rand, shape, scale float64) float64 {
	if shape < 1 {
		return GammaRandom(rnd, shape+1, scale) * math.Pow(rnd.Float64(), 1.0/shape)
	}

	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x := 0.0
		v := 0.0
		for {
			x = rnd.NormFloat64()
			v = 1.0 + c*x
			if v > 0 {
				break
			}
		}

		v = v * v * v
		u := rnd.Float64()

		if u < 1.0-0.331*math.Pow(x, 4) {
			return d * v * scale
		}

		if math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return d * v * scale
		}
	}
}

// ExponentialRandom generates exponentially distributed random numbers
func ExponentialRandom(rnd *rand.Rand, rate float64) float64 {
	return -math.Log(1.0-rnd.Float64()) / rate
}

// NormalRandom generates normally distributed random numbers
func NormalRandom(rnd *rand.Rand, mean, stddev float64) float64 {
	return rnd.NormFloat64()*stddev + mean
}

// LogNormalRandom generates log-normally distributed random numbers
func LogNormalRandom(rnd *rand.Rand, mu, sigma float64) float64 {
	return math.Exp(NormalRandom(rnd, mu, sigma))
}

// WeibullRandom generates Weibull distributed random numbers
func WeibullRandom(rnd *rand.Rand, shape, scale float64) float64 {
	return scale * math.Pow(-math.Log(1.0-rnd.Float64()), 1.0/shape)
}

// BetaRandom generates Beta distributed random numbers
func BetaRandom(rnd *rand.Rand, alpha, beta float64) float64 {
	x := GammaRandom(rnd, alpha, 1.0)
	y := GammaRandom(rnd, beta, 1.0)
	return x / (x + y)
}

// SinusoidalValue generates sinusoidal values for cyclical metrics
func SinusoidalValue(rnd *rand.Rand, timestamp time.Time, amplitude, period, phase, baseline float64) float64 {
	t := float64(timestamp.Unix())
	noise := rnd.NormFloat64() * amplitude * 0.1 // 10% noise
	return baseline + amplitude*math.Sin(2*math.Pi*t/period+phase) + noise
}

// SpikyValue generates values with occasional spikes
func SpikyValue(rnd *rand.Rand, baseline, spikeHeight, spikeProb float64) float64 {
	if rnd.Float64() < spikeProb {
		return baseline + spikeHeight*rnd.Float64()
	}
	return baseline + rnd.NormFloat64()*baseline*0.1
}

// TrendingValue generates values with an upward or downward trend
func TrendingValue(rnd *rand.Rand, timestamp time.Time, startValue, trendRate, noise float64) float64 {
	t := float64(timestamp.Unix())
	trend := startValue + trendRate*t
	return trend + rnd.NormFloat64()*noise
}

func main() {
	// Create generator
	generator := NewGenerator("localhost:4318")

	// Add HTTP request counter with realistic patterns
	generator.AddMetric(MetricDefinition{
		Name:        "http_requests_total",
		Type:        TypeSum,
		Description: "Total HTTP requests with realistic traffic patterns",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			// Simulate daily traffic pattern with spikes
			return SinusoidalValue(rnd, timestamp, 50, 86400, 0, 100) // 24-hour cycle
		},
	})

	// Add CPU usage with realistic variations
	generator.AddMetric(MetricDefinition{
		Name:        "cpu_usage_percent",
		Type:        TypeGauge,
		Description: "CPU usage with realistic variations",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			// CPU usage between 20-80% with occasional spikes
			baseline := SinusoidalValue(rnd, timestamp, 20, 3600, 0, 50)           // 1-hour cycle
			return math.Max(0, math.Min(100, SpikyValue(rnd, baseline, 30, 0.05))) // 5% spike chance
		},
	})

	// Add memory usage with gradual increase (memory leak simulation)
	generator.AddMetric(MetricDefinition{
		Name:        "memory_usage_bytes",
		Type:        TypeGauge,
		Description: "Memory usage with gradual increase",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			// Simulate memory leak: gradual increase with noise
			return TrendingValue(rnd, timestamp, 1e9, 1e6, 1e8) // Start at 1GB, increase 1MB/sec
		},
	})

	// Add response time histogram with realistic distribution
	generator.AddMetric(MetricDefinition{
		Name:        "http_response_time_seconds",
		Type:        TypeHistogram,
		Description: "HTTP response times with realistic distribution",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			// Most requests are fast, some are slow (long tail)
			if rnd.Float64() < 0.95 {
				// 95% of requests: fast (Gamma distribution)
				return GammaRandom(rnd, 2.0, 0.05) // Mean ~0.1s
			} else {
				// 5% of requests: slow (different Gamma)
				return GammaRandom(rnd, 1.5, 0.5) // Mean ~0.75s
			}
		},
	})

	// Add database connection pool
	generator.AddMetric(MetricDefinition{
		Name:        "db_connections_active",
		Type:        TypeGauge,
		Description: "Active database connections",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			// Connection pool usage follows traffic pattern
			traffic := SinusoidalValue(rnd, timestamp, 15, 86400, 0, 25) // 24-hour cycle
			return math.Max(1, traffic)                                  // At least 1 connection
		},
	})

	// Add error rate
	generator.AddMetric(MetricDefinition{
		Name:        "error_rate_percent",
		Type:        TypeGauge,
		Description: "Error rate percentage",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			// Usually low error rate with occasional incidents
			baseline := 0.5 + rnd.Float64()*0.5        // 0.5-1% baseline
			return SpikyValue(rnd, baseline, 10, 0.02) // 2% chance of incident
		},
	})

	// Add disk I/O with Weibull distribution
	generator.AddMetric(MetricDefinition{
		Name:        "disk_io_operations_per_second",
		Type:        TypeGauge,
		Description: "Disk I/O operations per second",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			return WeibullRandom(rnd, 2.0, 100) // Weibull distribution for I/O
		},
	})

	// Add network throughput with log-normal distribution
	generator.AddMetric(MetricDefinition{
		Name:        "network_throughput_mbps",
		Type:        TypeGauge,
		Description: "Network throughput in Mbps",
		ValueFunc: func(rnd *rand.Rand, timestamp time.Time) float64 {
			return LogNormalRandom(rnd, 3.0, 0.5) // Log-normal for network traffic
		},
	})

	log.Println("Starting advanced metric generation with telemetrygen...")

	// Generate metrics every 5 seconds for 2 minutes
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(2 * time.Minute)

	for {
		select {
		case <-ticker.C:
			if err := generator.GenerateMetrics(); err != nil {
				log.Printf("Error generating metrics: %v", err)
			}
		case <-timeout:
			log.Println("Metric generation completed")
			return
		}
	}
}
