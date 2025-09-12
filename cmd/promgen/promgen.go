package main

import (
	"log"
	"maps"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/share/testdata/histograms"
	"github.com/prometheus/client_golang/prometheus"
)

const updatePeriod = time.Second

func main() {

	start := time.Now()
	generator := NewGenerator()

	monotonicCounter := MetricDefinition{
		Name: "monotonic_counter",
		Type: TypeCounter,
		Help: "A counter that increases forever",
		Update: func(collector prometheus.Collector, timestamp time.Time) error {
			counter, err := collector.(*prometheus.CounterVec).GetMetricWith(prometheus.Labels{})
			if err != nil {
				return err
			}
			counter.Inc()
			return nil
		},
	}

	if err := generator.AddMetric(monotonicCounter); err != nil {
		log.Fatalf("unable to add metric: %v", err)
	}

	sinusoidalGauge := MetricDefinition{
		Name: "sinusoidal_gauge",
		Type: TypeGauge,
		Help: "A gauge that oscillates between -1 and 1",
		Update: func(collector prometheus.Collector, timestamp time.Time) error {
			gauge, err := collector.(*prometheus.GaugeVec).GetMetricWith(prometheus.Labels{})
			if err != nil {
				return err
			}
			newVal := math.Sin(2 * math.Pi * float64(timestamp.Unix()) / 20)
			gauge.Set(newVal)
			return nil
		},
	}

	if err := generator.AddMetric(sinusoidalGauge); err != nil {
		log.Fatalf("unable to add metric: %v", err)
	}

	gammaHistogram := MetricDefinition{
		Name: "gamma_histogram",
		Type: TypeHistogram,
		Help: "A histogram whose values follow a gamma distribution",
		Update: func(collector prometheus.Collector, timestamp time.Time) error {
			histogram, err := collector.(*prometheus.HistogramVec).GetMetricWith(prometheus.Labels{})
			if err != nil {
				return err
			}
			numObservations := generator.rand.Int() % 10
			for range numObservations {
				histogram.Observe(GammaRandom(generator.rand, 2.0, 2.0))
			}
			return nil
		},
	}

	if err := generator.AddMetric(gammaHistogram); err != nil {
		log.Fatalf("unable to add metric: %v", err)
	}

	exponentialSummary := MetricDefinition{
		Name: "exponential_summary",
		Type: TypeSummary,
		Help: "A summary whose values follow an exponential distribution",
		Update: func(collector prometheus.Collector, timestamp time.Time) error {
			summary, err := collector.(*prometheus.SummaryVec).GetMetricWith(prometheus.Labels{})
			if err != nil {
				return err
			}
			numObservations := generator.rand.Int() % 10
			for range numObservations {
				summary.Observe(generator.rand.ExpFloat64())
			}
			return nil
		},
	}

	if err := generator.AddMetric(exponentialSummary); err != nil {
		log.Fatalf("unable to add metric: %v", err)
	}

	gammaNativeHistogram := MetricDefinition{
		Name: "gamma_native_histogram",
		Type: TypeNativeHistogram,
		Help: "A native histogram whose values follow a gamma distribution",
		Update: func(collector prometheus.Collector, timestamp time.Time) error {
			histogram, err := collector.(*prometheus.HistogramVec).GetMetricWith(prometheus.Labels{})
			if err != nil {
				return err
			}
			numObservations := generator.rand.Int() % 10
			for range numObservations {
				histogram.Observe(GammaRandom(generator.rand, 2.0, 2.0))
			}
			return nil
		},
	}

	if err := generator.AddMetric(gammaNativeHistogram); err != nil {
		log.Fatalf("unable to add metric: %v", err)
	}

	testCases := histograms.TestCases()
	for _, tc := range testCases {
		tName := "tc_" + strings.ToLower(strings.ReplaceAll(tc.Name, " ", "_"))
		tMetricDefinition := MetricDefinition{
			Name: tName,
			Type: TypeHistogram,
			Help: tc.Name,
			CreateCollector: func() (prometheus.Collector, error) {
				// prometheus gives default buckets if boundaries is empty. we want one big bucket instead
				boundaries := tc.Input.Boundaries
				if len(boundaries) == 0 {
					boundaries = []float64{math.Inf(1)}
				}

				return prometheus.NewHistogramVec(
					prometheus.HistogramOpts{
						Name:    tName,
						Help:    "My first test case",
						Buckets: boundaries,
					},
					slices.Collect(maps.Keys(tc.Input.Attributes)),
				), nil
			},
			Update: func(collector prometheus.Collector, timestamp time.Time) error {
				// Only update once
				if time.Since(start) > 2*updatePeriod {
					return nil
				}
				histogram, err := collector.(*prometheus.HistogramVec).GetMetricWith(tc.Input.Attributes)
				if err != nil {
					return err
				}
				for _, v := range generateDatapoints(tc.Input) {
					histogram.Observe(v)
				}
				return nil
			},
		}

		if err := generator.AddMetric(tMetricDefinition); err != nil {
			log.Fatalf("unable to add metric: %v", err)
		}
	}

	// Start updating metrics periodically
	go func() {
		ticker := time.NewTicker(updatePeriod)
		defer ticker.Stop()
		for t := range ticker.C {
			if err := generator.UpdateMetrics(t); err != nil {
				log.Printf("Error updating metrics: %v", err)
			}
		}
	}()

	// Start HTTP server
	http.Handle("/metrics", generator)
	log.Printf("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func generateDatapoints(in histograms.HistogramInput) []float64 {
	if in.Count == 0 {
		return []float64{}
	}

	dps := []float64{}
	totalGenerated := 0.0

	for i, count := range in.Counts {
		if count == 0 {
			continue
		}

		var bucketValue float64
		if len(in.Boundaries) == 0 {
			bucketValue = in.Sum / float64(in.Count)
		} else if i == 0 {
			if in.Min != nil {
				bucketValue = (*in.Min + in.Boundaries[0]) / 2
			} else {
				bucketValue = in.Boundaries[0] - 1
			}
		} else if i < len(in.Boundaries) {
			bucketValue = (in.Boundaries[i-1] + in.Boundaries[i]) / 2
		} else {
			if in.Max != nil {
				bucketValue = (in.Boundaries[len(in.Boundaries)-1] + *in.Max) / 2
			} else {
				bucketValue = in.Boundaries[len(in.Boundaries)-1] + 1
			}
		}

		for j := uint64(0); j < count; j++ {
			dps = append(dps, bucketValue)
			totalGenerated += bucketValue
		}
	}

	if len(dps) > 0 && totalGenerated != 0 && len(in.Boundaries) > 0 {
		ratio := in.Sum / totalGenerated
		for i := range dps {
			dps[i] *= ratio
		}
	}
	return dps
}
