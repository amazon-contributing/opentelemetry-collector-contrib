package generator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"time"
)

// PercentileRange represents a range for percentile calculations
type PercentileRange struct {
	Low  float64
	High float64
}

// HistogramInput represents the input data for a histogram metric
type HistogramInput struct {
	Count      uint64
	Sum        float64
	Min        *float64
	Max        *float64
	Boundaries []float64
	Counts     []uint64
	Attributes map[string]string
}

// ExpectedMetrics represents the expected calculated metrics from histogram data
type ExpectedMetrics struct {
	Count            uint64
	Sum              float64
	Average          float64
	Min              *float64
	Max              *float64
	PercentileRanges map[float64]PercentileRange
}

// HistogramResult combines input and expected metrics
type HistogramResult struct {
	Input    HistogramInput
	Expected ExpectedMetrics
}

// HistogramGenerator generates histogram test cases using statistical distributions
type HistogramGenerator struct {
	rand     *rand.Rand
	endpoint string
}

type GenerationOptions struct {
	Seed     int64
	Endpoint string
}

// NewHistogramGenerator creates a new histogram generator with deterministic seed
func NewHistogramGenerator(opt ...GenerationOptions) *HistogramGenerator {
    var seed int64 = time.Now().UnixNano()
    var endpoint string

    if len(opt) > 0 {
        if opt[0].Seed != 0 {
            seed = opt[0].Seed
        }
        endpoint = opt[0].Endpoint // empty string is the zero value
    }

    return &HistogramGenerator{
        rand:     rand.New(rand.NewSource(seed)),
        endpoint: endpoint, // store as string, check for empty when using
    }
}


// GenerateHistogram generates histogram data from individual values using a value function
func (g *HistogramGenerator) GenerateHistogram(input HistogramInput, valueFunc func(*rand.Rand, time.Time) float64) (HistogramResult, error) {
	timestamp := time.Now()
	sampleCount := int(input.Count)

	if sampleCount <= 0 {
		sampleCount = 1000 // default sample count
	}

	// Generate individual values using the value function
	values := make([]float64, sampleCount)
	for i := 0; i < sampleCount; i++ {
		if valueFunc != nil {
			values[i] = valueFunc(g.rand, timestamp)
		} else {
			values[i] = g.rand.Float64() * 100 // default random value
		}
	}

	// Sort values to find min/max
	sort.Float64s(values)

	// Calculate basic stats
	var sum float64
	for _, v := range values {
		sum += v
	}

	generatedMin := values[0]
	generatedMax := values[len(values)-1]
	average := sum / float64(len(values))

	// Determine final min/max values
	var finalMin, finalMax float64
	if input.Min != nil {
		finalMin = *input.Min
	} else {
		finalMin = generatedMin
	}
	if input.Max != nil {
		finalMax = *input.Max
	} else {
		finalMax = generatedMax
	}

	// Use provided boundaries or generate them based on min/max
	boundaries := input.Boundaries
	if len(boundaries) == 0 {
		// Generate boundaries based on final min/max
		boundaries = generateBoundariesBetween(finalMin, finalMax, 10)
	}

	counts := make([]uint64, len(boundaries)+1)
	for _, value := range values {
		bucketIndex := len(boundaries) // default to overflow bucket
		for i, boundary := range boundaries {
			if value <= boundary {
				bucketIndex = i
				break
			}
		}
		counts[bucketIndex]++
	}

	// Calculate percentile ranges
	percentileRanges := g.calculatePercentileRangesFromValues(values, boundaries)

	// Use input min/max if provided, otherwise use generated values
	var resultMin, resultMax *float64
	if input.Min != nil {
		resultMin = input.Min
	} else {
		resultMin = &generatedMin
	}
	if input.Max != nil {
		resultMax = input.Max
	} else {
		resultMax = &generatedMax
	}

	generatedInput := HistogramInput{
		Count:      uint64(len(values)),
		Sum:        sum,
		Min:        resultMin,
		Max:        resultMax,
		Boundaries: boundaries,
		Counts:     counts,
		Attributes: input.Attributes,
	}

	expected := ExpectedMetrics{
		Count:            uint64(len(values)),
		Sum:              sum,
		Average:          average,
		Min:              resultMin,
		Max:              resultMax,
		PercentileRanges: percentileRanges,
	}

	return HistogramResult{
		Input:    generatedInput,
		Expected: expected,
	}, nil
}

func (g * HistogramGenerator) GenerateAndPublishHistograms(input HistogramInput, valueFunc func(*rand.Rand, time.Time) float64) (HistogramResult, error) {
	res, err :=g.GenerateHistogram(input,valueFunc)
	if err != nil {
		return HistogramResult{}, err
	}
	if g.endpoint == "" {
		return res, nil
	}
	err = sendHistogramMetric(g.endpoint, "TelemetryGen",res)
	if err != nil {
		return HistogramResult{}, err
	}
	return res, nil
}

// calculatePercentileRangesFromValues calculates percentile ranges for sorted values
func (g *HistogramGenerator) calculatePercentileRangesFromValues(sortedValues []float64, boundaries []float64) map[float64]PercentileRange {
	percentiles := []float64{0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99}
	ranges := make(map[float64]PercentileRange)

	for _, p := range percentiles {
		index := int(p * float64(len(sortedValues)))
		if index >= len(sortedValues) {
			index = len(sortedValues) - 1
		}

		value := sortedValues[index]

		// Find which bucket this percentile value falls into
		var low, high float64

		// Check if value falls in any boundary bucket
		bucketFound := false
		for i, boundary := range boundaries {
			if value <= boundary {
				if i > 0 {
					low = boundaries[i-1]
				} else {
					low = math.Inf(-1)
				}
				high = boundary
				bucketFound = true
				break
			}
		}

		// If not found in any boundary bucket, it's in the overflow bucket
		if !bucketFound {
			if len(boundaries) > 0 {
				low = boundaries[len(boundaries)-1]
			} else {
				low = math.Inf(-1)
			}
			high = math.Inf(1)
		}

		ranges[p] = PercentileRange{Low: low, High: high}
	}

	return ranges
}

// generateBoundariesBetween creates evenly spaced boundaries between min and max
func generateBoundariesBetween(min, max float64, numBuckets int) []float64 {
	if numBuckets <= 0 {
		numBuckets = 10
	}

	boundaries := make([]float64, numBuckets-1)
	step := (max - min) / float64(numBuckets)

	for i := 0; i < numBuckets-1; i++ {
		boundaries[i] = min + float64(i+1)*step
	}

	return boundaries
}

// Distribution functions
func ExponentialRandom(rnd *rand.Rand, rate float64) float64 {
	return -math.Log(1.0-rnd.Float64()) / rate
}

func NormalRandom(rnd *rand.Rand, mean, stddev float64) float64 {
	return rnd.NormFloat64()*stddev + mean
}

func LogNormalRandom(rnd *rand.Rand, mu, sigma float64) float64 {
	return math.Exp(NormalRandom(rnd, mu, sigma))
}

func WeibullRandom(rnd *rand.Rand, shape, scale float64) float64 {
	return scale * math.Pow(-math.Log(1.0-rnd.Float64()), 1.0/shape)
}

func BetaRandom(rnd *rand.Rand, alpha, beta float64) float64 {
	x := GammaRandom(rnd, alpha, 1.0)
	y := GammaRandom(rnd, beta, 1.0)
	return x / (x + y)
}

func SinusoidalValue(rnd *rand.Rand, timestamp time.Time, amplitude, period, phase, baseline float64) float64 {
	t := float64(timestamp.Unix())
	noise := rnd.NormFloat64() * amplitude * 0.1 // 10% noise
	return baseline + amplitude*math.Sin(2*math.Pi*t/period+phase) + noise
}

func SpikyValue(rnd *rand.Rand, baseline, spikeHeight, spikeProb float64) float64 {
	if rnd.Float64() < spikeProb {
		return baseline + spikeHeight*rnd.Float64()
	}
	return baseline + rnd.NormFloat64()*baseline*0.1
}

func TrendingValue(rnd *rand.Rand, timestamp time.Time, startValue, trendRate, noise float64) float64 {
	t := float64(timestamp.Unix())
	trend := startValue + trendRate*t
	return trend + rnd.NormFloat64()*noise
}

func GammaRandom(rnd *rand.Rand, alpha, beta float64) float64 {
	if alpha < 1.0 {
		// Use Johnk's generator for alpha < 1
		for {
			u := rnd.Float64()
			v := rnd.Float64()
			x := math.Pow(u, 1.0/alpha)
			y := math.Pow(v, 1.0/(1.0-alpha))
			if x+y <= 1.0 {
				if x+y > 0 {
					return beta * x / (x + y) * (-math.Log(rnd.Float64()))
				}
			}
		}
	}

	// Marsaglia and Tsang's method for alpha >= 1
	d := alpha - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x := rnd.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}
		v = v * v * v
		u := rnd.Float64()
		if u < 1.0-0.0331*(x*x)*(x*x) {
			return beta * d * v
		}
		if math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return beta * d * v
		}
	}
}


func sendHistogramMetric(endpoint, metricName string, result HistogramResult) error {
	// Create OTLP histogram metric payload
	timestamp := time.Now().UnixNano()

	// Build bucket counts array
	bucketCounts := make([]uint64, len(result.Input.Boundaries)+1)
	copy(bucketCounts, result.Input.Counts)

	// Build explicit bounds array
	explicitBounds := make([]float64, len(result.Input.Boundaries))
	copy(explicitBounds, result.Input.Boundaries)

	payload := map[string]interface{}{
		"resourceMetrics": []map[string]interface{}{
			{
				"resource": map[string]interface{}{
					"attributes": []map[string]interface{}{
						{
							"key":   "service.name",
							"value": map[string]interface{}{"stringValue": result.Input.Attributes["service.name"]},
						},
						{
							"key":   "service.version",
							"value": map[string]interface{}{"stringValue": result.Input.Attributes["service.version"]},
						},
					},
				},
				"scopeMetrics": []map[string]interface{}{
					{
						"scope": map[string]interface{}{
							"name":    "histogram-generator",
							"version": "1.0.0",
						},
						"metrics": []map[string]interface{}{
							{
								"name":        metricName,
								"description": "Generated histogram metric",
								"unit":        "ms",
								"histogram": map[string]interface{}{
									"dataPoints": []map[string]interface{}{
										{
											"attributes": []map[string]interface{}{
												{
													"key":   "environment",
													"value": map[string]interface{}{"stringValue": result.Input.Attributes["environment"]},
												},
											},
											"timeUnixNano":   fmt.Sprintf("%d", timestamp),
											"count":          fmt.Sprintf("%d", result.Input.Count),
											"sum":            result.Input.Sum,
											"bucketCounts":   bucketCounts,
											"explicitBounds": explicitBounds,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	// Send HTTP POST to OTLP endpoint
	url := fmt.Sprintf("http://%s/v1/metrics", endpoint)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 status: %d", resp.StatusCode)
	}

	return nil
}
