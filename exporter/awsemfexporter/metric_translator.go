// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter"

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter/internal/entity"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/cwlogs"
	aws "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/metrics"
)

const (
	// OTel instrumentation lib name as dimension
	oTellibDimensionKey = "OTelLib"
	defaultNamespace    = "default"

	// DimensionRollupOptions
	zeroAndSingleDimensionRollup = "ZeroAndSingleDimensionRollup"
	singleDimensionRollupOnly    = "SingleDimensionRollupOnly"

	prometheusReceiver        = "prometheus"
	containerInsightsReceiver = "awscontainerinsight"
	attributeReceiver         = "receiver"
	fieldPrometheusMetricType = "prom_metric_type"

	// metric attributes for AWS EMF, not to be treated as metric labels
	emfStorageResolutionAttribute = "aws.emf.storage_resolution"
)

var errMissingMetricsForEnhancedContainerInsights = errors.New("nil event detected with EnhancedContainerInsights enabled")

var fieldPrometheusTypes = map[pmetric.MetricType]string{
	pmetric.MetricTypeEmpty:     "",
	pmetric.MetricTypeGauge:     "gauge",
	pmetric.MetricTypeSum:       "counter",
	pmetric.MetricTypeHistogram: "histogram",
	pmetric.MetricTypeSummary:   "summary",
}

type cWMetrics struct {
	measurements []cWMeasurement
	timestampMs  int64
	fields       map[string]any
}

type cWMetricInfo struct {
	Name              string
	Unit              string
	StorageResolution int
}

type cWMeasurement struct {
	Namespace  string
	Dimensions [][]string
	Metrics    []cWMetricInfo
}

type cWMetricStats struct {
	Max   float64
	Min   float64
	Count uint64
	Sum   float64
}

// The SampleCount of CloudWatch metrics will be calculated by the sum of the 'Counts' array.
// The 'Count' field should be same as the sum of the 'Counts' array and will be ignored in CloudWatch.
type cWMetricHistogram struct {
	Values []float64
	Counts []float64
	Max    float64
	Min    float64
	Count  uint64
	Sum    float64
}

type groupedMetricMetadata struct {
	namespace                  string
	timestampMs                int64
	logGroup                   string
	logStream                  string
	metricDataType             pmetric.MetricType
	batchIndex                 int
	retainInitialValueForDelta bool
}

// cWMetricMetadata represents the metadata associated with a given CloudWatch metric
type cWMetricMetadata struct {
	groupedMetricMetadata
	instrumentationScopeName string
	receiver                 string
}

type metricTranslator struct {
	metricDescriptor map[string]MetricDescriptor
	calculators      *emfCalculators
}

func newMetricTranslator(config Config) metricTranslator {
	mt := map[string]MetricDescriptor{}
	for _, descriptor := range config.MetricDescriptors {
		mt[descriptor.MetricName] = descriptor
	}
	return metricTranslator{
		metricDescriptor: mt,
		calculators: &emfCalculators{
			delta:   aws.NewFloat64DeltaCalculator(),
			summary: aws.NewMetricCalculator(calculateSummaryDelta),
		},
	}
}

func (mt metricTranslator) Shutdown() error {
	var errs error
	errs = multierr.Append(errs, mt.calculators.delta.Shutdown())
	errs = multierr.Append(errs, mt.calculators.summary.Shutdown())
	return errs
}

// translateOTelToGroupedMetric converts OT metrics to Grouped Metric format.
func (mt metricTranslator) translateOTelToGroupedMetric(rm pmetric.ResourceMetrics, groupedMetrics map[any]*groupedMetric, config *Config) error {
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	var instrumentationScopeName string
	cWNamespace := getNamespace(rm, config.Namespace)
	logGroup, logStream, patternReplaceSucceeded := getLogInfo(rm, cWNamespace, config)
	deltaInitialValue := config.RetainInitialValueOfDeltaMetric

	ilms := rm.ScopeMetrics()
	var metricReceiver string
	if receiver, ok := rm.Resource().Attributes().Get(attributeReceiver); ok {
		metricReceiver = receiver.Str()
	}

	if serviceName, ok := rm.Resource().Attributes().Get("service.name"); ok {
		if strings.HasPrefix(serviceName.Str(), "containerInsightsKubeAPIServerScraper") ||
			strings.HasPrefix(serviceName.Str(), "containerInsightsDCGMExporterScraper") ||
			strings.HasPrefix(serviceName.Str(), "containerInsightsNeuronMonitorScraper") ||
			strings.HasPrefix(serviceName.Str(), "containerInsightsKueueMetricsScraper") {
			// the prometheus metrics that come from the container insight receiver need to be clearly tagged as coming from container insights
			metricReceiver = containerInsightsReceiver
		}
	}

	for j := 0; j < ilms.Len(); j++ {
		ilm := ilms.At(j)
		if ilm.Scope().Name() != "" {
			instrumentationScopeName = ilm.Scope().Name()
		}

		metrics := ilm.Metrics()
		for k := 0; k < metrics.Len(); k++ {
			metric := metrics.At(k)
			metadata := cWMetricMetadata{
				groupedMetricMetadata: groupedMetricMetadata{
					namespace:                  cWNamespace,
					timestampMs:                timestamp,
					logGroup:                   logGroup,
					logStream:                  logStream,
					metricDataType:             metric.Type(),
					batchIndex:                 0,
					retainInitialValueForDelta: deltaInitialValue,
				},
				instrumentationScopeName: instrumentationScopeName,
				receiver:                 metricReceiver,
			}
			err := addToGroupedMetric(metric, groupedMetrics, metadata, patternReplaceSucceeded, mt.metricDescriptor, config, mt.calculators)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// translateGroupedMetricToCWMetric converts Grouped Metric format to CloudWatch Metric format.
func translateGroupedMetricToCWMetric(groupedMetric *groupedMetric, config *Config) *cWMetrics {
	labels := filterAWSEMFAttributes(groupedMetric.labels, false)
	fieldsLength := len(labels) + len(groupedMetric.metrics)

	isPrometheusMetric := groupedMetric.metadata.receiver == prometheusReceiver
	if isPrometheusMetric {
		fieldsLength++
	}
	fields := make(map[string]any, fieldsLength)

	// Add labels to fields
	for k, v := range labels {
		if !strings.HasPrefix(k, entity.AWSEntityPrefix) {
			fields[k] = v
			continue
		}

		if config.AddEntity {
			// This check is needed to determine whether to use EKS.Cluster or K8s.Cluster
			if k == entity.AttributeEntityK8sClusterName {
				if entityField := entity.GetEntityField(k, labels[entity.AttributeEntityPlatformType]); entityField != "" {
					fields[entityField] = v
				}
				continue
			}

			if entityField := entity.GetEntityField(k, ""); entityField != "" {
				fields[entityField] = v
			}
		}
	}
	// Add metrics to fields
	for metricName, metricInfo := range groupedMetric.metrics {
		fields[metricName] = metricInfo.value
	}
	if isPrometheusMetric {
		fields[fieldPrometheusMetricType] = fieldPrometheusTypes[groupedMetric.metadata.metricDataType]
	}

	var cWMeasurements []cWMeasurement
	if !config.DisableMetricExtraction { // If metric extraction is disabled, there is no need to compute & set the measurements
		if len(config.MetricDeclarations) == 0 {
			// If there are no metric declarations defined, translate grouped metric
			// into the corresponding CW Measurement
			cwm := groupedMetricToCWMeasurement(groupedMetric, config)
			cWMeasurements = []cWMeasurement{cwm}
		} else {
			// If metric declarations are defined, filter grouped metric's metrics using
			// metric declarations and translate into the corresponding list of CW Measurements
			cWMeasurements = groupedMetricToCWMeasurementsWithFilters(groupedMetric, config)
		}
	}

	return &cWMetrics{
		measurements: cWMeasurements,
		timestampMs:  groupedMetric.metadata.timestampMs,
		fields:       fields,
	}
}

// groupedMetricToCWMeasurement creates a single CW Measurement from a grouped metric.
func groupedMetricToCWMeasurement(groupedMetric *groupedMetric, config *Config) cWMeasurement {
	labels := filterAWSEMFAttributes(groupedMetric.labels, true)

	dimensionRollupOption := config.DimensionRollupOption

	// Create a dimension set containing list of label names
	dimSet := make([]string, len(labels))
	idx := 0
	for labelName := range labels {
		dimSet[idx] = labelName
		idx++
	}

	dimensions := [][]string{dimSet}

	// Apply single/zero dimension rollup to labels
	rollupDimensionArray := dimensionRollup(dimensionRollupOption, labels)

	if len(rollupDimensionArray) > 0 {
		// Perform duplication check for edge case with a single label and single dimension roll-up
		_, hasOTelLibKey := labels[oTellibDimensionKey]
		isSingleLabel := len(dimSet) <= 1 || (len(dimSet) == 2 && hasOTelLibKey)
		singleDimRollup := dimensionRollupOption == singleDimensionRollupOnly ||
			dimensionRollupOption == zeroAndSingleDimensionRollup
		if isSingleLabel && singleDimRollup {
			// Remove duplicated dimension set before adding on rolled-up dimensions
			dimensions = nil
		}
	}

	// Add on rolled-up dimensions
	dimensions = append(dimensions, rollupDimensionArray...)

	metrics := make([]cWMetricInfo, len(groupedMetric.metrics))
	idx = 0
	for metricName, metricInfo := range groupedMetric.metrics {
		metrics[idx] = cWMetricInfo{
			Name:              metricName,
			StorageResolution: 60,
		}
		if metricInfo.unit != "" {
			metrics[idx].Unit = metricInfo.unit
		}
		if storRes, ok := groupedMetric.labels[emfStorageResolutionAttribute]; ok {
			if storResInt, err := strconv.Atoi(storRes); err == nil {
				metrics[idx].StorageResolution = storResInt
			}
		}
		idx++
	}

	return cWMeasurement{
		Namespace:  groupedMetric.metadata.namespace,
		Dimensions: dimensions,
		Metrics:    metrics,
	}
}

// groupedMetricToCWMeasurementsWithFilters filters the grouped metric using the given list of metric
// declarations and returns the corresponding list of CW Measurements.
func groupedMetricToCWMeasurementsWithFilters(groupedMetric *groupedMetric, config *Config) (cWMeasurements []cWMeasurement) {
	labels := filterAWSEMFAttributes(groupedMetric.labels, true)

	// Filter metric declarations by labels
	metricDeclarations := make([]*MetricDeclaration, 0, len(config.MetricDeclarations))
	for _, metricDeclaration := range config.MetricDeclarations {
		if metricDeclaration.MatchesLabels(labels) {
			metricDeclarations = append(metricDeclarations, metricDeclaration)
		}
	}

	// If the whole batch of metrics don't match any metric declarations, drop them
	if len(metricDeclarations) == 0 {
		labelsStr, _ := json.Marshal(labels)
		var metricNames []string
		for metricName := range groupedMetric.metrics {
			metricNames = append(metricNames, metricName)
		}
		config.logger.Debug(
			"Dropped batch of metrics: no metric declaration matched labels",
			zap.String("Labels", string(labelsStr)),
			zap.Strings("Metric Names", metricNames),
		)
		return
	}

	// Group metrics by matched metric declarations
	type metricDeclarationGroup struct {
		metricDeclIdxList []int
		metrics           []cWMetricInfo
	}

	metricDeclGroups := make(map[string]*metricDeclarationGroup)
	for metricName, metricInfo := range groupedMetric.metrics {
		// Filter metric declarations by metric name
		var metricDeclIdx []int
		for i, metricDeclaration := range metricDeclarations {
			if metricDeclaration.MatchesName(metricName) {
				metricDeclIdx = append(metricDeclIdx, i)
			}
		}

		if len(metricDeclIdx) == 0 {
			config.logger.Debug(
				"Dropped metric: no metric declaration matched metric name",
				zap.String("Metric name", metricName),
			)
			continue
		}

		metric := cWMetricInfo{
			Name:              metricName,
			StorageResolution: 60,
		}
		if metricInfo.unit != "" {
			metric.Unit = metricInfo.unit
		}
		if storRes, ok := groupedMetric.labels[emfStorageResolutionAttribute]; ok {
			if storResInt, err := strconv.Atoi(storRes); err == nil {
				metric.StorageResolution = storResInt
			}
		}
		metricDeclKey := fmt.Sprint(metricDeclIdx)
		if group, ok := metricDeclGroups[metricDeclKey]; ok {
			group.metrics = append(group.metrics, metric)
		} else {
			metricDeclGroups[metricDeclKey] = &metricDeclarationGroup{
				metricDeclIdxList: metricDeclIdx,
				metrics:           []cWMetricInfo{metric},
			}
		}
	}

	if len(metricDeclGroups) == 0 {
		return
	}

	// Apply single/zero dimension rollup to labels
	rollupDimensionArray := dimensionRollup(config.DimensionRollupOption, labels)

	// Translate each group into a CW Measurement
	cWMeasurements = make([]cWMeasurement, 0, len(metricDeclGroups))
	for _, group := range metricDeclGroups {
		var dimensions [][]string
		// Extract dimensions from matched metric declarations
		for _, metricDeclIdx := range group.metricDeclIdxList {
			dims := metricDeclarations[metricDeclIdx].ExtractDimensions(labels)
			dimensions = append(dimensions, dims...)
		}
		dimensions = append(dimensions, rollupDimensionArray...)

		// De-duplicate dimensions
		dimensions = dedupDimensions(dimensions)

		// Export metrics only with non-empty dimensions list
		if len(dimensions) > 0 {
			cwm := cWMeasurement{
				Namespace:  groupedMetric.metadata.namespace,
				Dimensions: dimensions,
				Metrics:    group.metrics,
			}
			cWMeasurements = append(cWMeasurements, cwm)
		}
	}

	return
}

// translateCWMetricToEMF converts CloudWatch Metric format to EMF.
func translateCWMetricToEMF(cWMetric *cWMetrics, config *Config) (*cwlogs.Event, error) {
	fmt.Println("\n=== START: translateCWMetricToEMF ===")
	fmt.Printf("Initial CWMetric fields: %+v\n", cWMetric.fields)
	fmt.Printf("Initial measurements count: %d\n", len(cWMetric.measurements))

	// convert CWMetric into map format for compatible with PLE input
	fieldMap := cWMetric.fields
	fmt.Printf("Field map initialized: %+v\n", fieldMap)

	// restore the json objects that are stored as string in attributes
	fmt.Println("\n=== Processing JSON Encoded Attributes ===")
	for _, key := range config.ParseJSONEncodedAttributeValues {
		fmt.Printf("Processing key: %s\n", key)

		if fieldMap[key] == nil {
			fmt.Printf("Key %s not found in fieldMap\n", key)
			continue
		}

		if val, ok := fieldMap[key].(string); ok {
			fmt.Printf("Found string value for key %s: %s\n", key, val)
			var f any
			err := json.Unmarshal([]byte(val), &f)
			if err != nil {
				fmt.Printf("ERROR unmarshaling JSON for key %s: %v\n", key, err)
				config.logger.Debug(
					"Failed to parse json-encoded string",
					zap.String("label key", key),
					zap.String("label value", val),
					zap.Error(err),
				)
				continue
			}
			fieldMap[key] = f
			fmt.Printf("Successfully parsed JSON for key %s: %+v\n", key, f)
		} else {
			fmt.Printf("Invalid type for key %s: expected string, got %T\n", key, fieldMap[key])
			config.logger.Debug(
				"Invalid json-encoded data. A string is expected",
				zap.Any("type", reflect.TypeOf(fieldMap[key])),
				zap.Any("value", reflect.ValueOf(fieldMap[key])),
			)
		}
	}

	fmt.Println("\n=== Processing Version and Timestamp ===")
	fmt.Printf("Config Version: %s\n", config.Version)
	// For backwards compatibility, if EMF v0, always include version & timestamp
	if config.Version == "0" {
		fieldMap["Version"] = "0"
		fieldMap["Timestamp"] = fmt.Sprint(cWMetric.timestampMs)
		fmt.Println("Added Version 0 and Timestamp to fieldMap")
	}

	fmt.Printf("\n=== Processing Measurements (count: %d) ===\n", len(cWMetric.measurements))
	fmt.Printf("DisableMetricExtraction: %v\n", config.DisableMetricExtraction)

	// Create EMF metrics if there are measurements
	if len(cWMetric.measurements) > 0 && !config.DisableMetricExtraction {
		if config.Version == "1" {
			fmt.Println("Processing EMF V1 format")
			fieldMap["Version"] = "1"
			fieldMap["_aws"] = map[string]any{
				"CloudWatchMetrics": cWMetric.measurements,
				"Timestamp":         cWMetric.timestampMs,
			}
			fmt.Printf("Added EMF V1 structure: %+v\n", fieldMap["_aws"])
		} else {
			fmt.Println("Processing EMF V0 format")
			fieldMap["CloudWatchMetrics"] = cWMetric.measurements
			fmt.Printf("Added CloudWatchMetrics: %+v\n", fieldMap["CloudWatchMetrics"])
		}
	} else if len(cWMetric.measurements) < 1 && config.EnhancedContainerInsights {
		fmt.Println("No metrics found with EnhancedContainerInsights enabled, returning nil")
		return nil, nil
	}

	fmt.Println("\n=== Processing Metrics Map ===")
	// remove metrics from fieldMap
	metricsMap := make(map[string]any)
	for _, measurement := range cWMetric.measurements {
		for _, metric := range measurement.Metrics {
			metricName := metric.Name
			fmt.Printf("Processing metric: %s\n", metricName)
			v, ok := fieldMap[metricName]
			if ok {
				metricsMap[metricName] = v
				delete(fieldMap, metricName)
				fmt.Printf("Moved metric %s to metricsMap\n", metricName)
			}
		}
	}

	fmt.Println("\n=== Marshaling Data ===")
	pleMsg, err := json.Marshal(fieldMap)
	if err != nil {
		fmt.Printf("ERROR marshaling fieldMap: %v\n", err)
		return nil, err
	}
	fmt.Printf("Marshaled fieldMap length: %d\n", len(pleMsg))

	// append metrics json to pleMsg
	if len(metricsMap) > 0 {
		fmt.Printf("Processing metricsMap with %d entries\n", len(metricsMap))
		metricsMsg, err := json.Marshal(metricsMap)
		if err != nil {
			fmt.Printf("ERROR marshaling metricsMap: %v\n", err)
			return nil, err
		}
		metricsMsg[0] = ','
		pleMsg = append(pleMsg[:len(pleMsg)-1], metricsMsg...)
		fmt.Printf("Final message length after adding metrics: %d\n", len(pleMsg))
	}

	fmt.Println("\n=== Creating Log Event ===")
	metricCreationTime := cWMetric.timestampMs
	fmt.Printf("Metric creation time: %d\n", metricCreationTime)

	logEvent := cwlogs.NewEvent(
		metricCreationTime,
		string(pleMsg),
	)
	logEvent.GeneratedTime = time.Unix(0, metricCreationTime*int64(time.Millisecond))

	fmt.Printf("Created log event with timestamp: %v\n", logEvent.GeneratedTime)
	fmt.Println("=== END: translateCWMetricToEMF ===\n")

	return logEvent, nil
}

// Utility function that converts from groupedMetric to a cloudwatch event
func translateGroupedMetricToEmf(groupedMetric *groupedMetric, config *Config, defaultLogStream string) (*cwlogs.Event, error) {
	cWMetric := translateGroupedMetricToCWMetric(groupedMetric, config)
	event, err := translateCWMetricToEMF(cWMetric, config)
	if err != nil {
		return nil, err
	}
	// Drop a nil putLogEvent for EnhancedContainerInsights
	if config.EnhancedContainerInsights && event == nil {
		return nil, errMissingMetricsForEnhancedContainerInsights
	}

	logGroup := groupedMetric.metadata.logGroup
	logStream := groupedMetric.metadata.logStream

	if logStream == "" {
		logStream = defaultLogStream
	}

	event.LogGroupName = logGroup
	event.LogStreamName = logStream

	return event, nil
}

func filterAWSEMFAttributes(labels map[string]string, removeEntity bool) map[string]string {
	// remove any labels that are attributes specific to AWS EMF Exporter
	filteredLabels := make(map[string]string)
	for labelName := range labels {
		if labelName != emfStorageResolutionAttribute &&
			(!removeEntity || !strings.HasPrefix(labelName, entity.AWSEntityPrefix)) {
			filteredLabels[labelName] = labels[labelName]
		}
	}
	return filteredLabels
}
