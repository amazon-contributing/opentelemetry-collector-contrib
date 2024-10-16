// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter"

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/internal/aws/cwlogs"
	aws "github.com/amazon-contributing/opentelemetry-collector-contrib/internal/aws/metrics"
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

type cWMeasurement struct {
	Namespace  string
	Dimensions [][]string
	Metrics    []map[string]string
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
			strings.HasPrefix(serviceName.Str(), "containerInsightsNeuronMonitorScraper") {
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
	labels := groupedMetric.labels
	fieldsLength := len(labels) + len(groupedMetric.metrics)

	isPrometheusMetric := groupedMetric.metadata.receiver == prometheusReceiver
	if isPrometheusMetric {
		fieldsLength++
	}
	fields := make(map[string]any, fieldsLength)

	// Add labels to fields
	for k, v := range labels {
		fields[k] = v
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
	labels := groupedMetric.labels
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

	metrics := make([]map[string]string, len(groupedMetric.metrics))
	idx = 0
	for metricName, metricInfo := range groupedMetric.metrics {
		metrics[idx] = map[string]string{
			"Name": metricName,
		}
		if metricInfo.unit != "" {
			metrics[idx]["Unit"] = metricInfo.unit
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
	labels := groupedMetric.labels

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
		metrics           []map[string]string
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

		metric := map[string]string{
			"Name": metricName,
		}
		if metricInfo.unit != "" {
			metric["Unit"] = metricInfo.unit
		}
		metricDeclKey := fmt.Sprint(metricDeclIdx)
		if group, ok := metricDeclGroups[metricDeclKey]; ok {
			group.metrics = append(group.metrics, metric)
		} else {
			metricDeclGroups[metricDeclKey] = &metricDeclarationGroup{
				metricDeclIdxList: metricDeclIdx,
				metrics:           []map[string]string{metric},
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
	// convert CWMetric into map format for compatible with PLE input
	fieldMap := cWMetric.fields

	// restore the json objects that are stored as string in attributes
	for _, key := range config.ParseJSONEncodedAttributeValues {
		if fieldMap[key] == nil {
			continue
		}

		if val, ok := fieldMap[key].(string); ok {
			var f any
			err := json.Unmarshal([]byte(val), &f)
			if err != nil {
				config.logger.Debug(
					"Failed to parse json-encoded string",
					zap.String("label key", key),
					zap.String("label value", val),
					zap.Error(err),
				)
				continue
			}
			fieldMap[key] = f
		} else {
			config.logger.Debug(
				"Invalid json-encoded data. A string is expected",
				zap.Any("type", reflect.TypeOf(fieldMap[key])),
				zap.Any("value", reflect.ValueOf(fieldMap[key])),
			)
		}
	}

	// For backwards compatibility, if EMF v0, always include version & timestamp (even for non-EMF events)
	if config.Version == "0" {
		fieldMap["Version"] = "0"
		fieldMap["Timestamp"] = fmt.Sprint(cWMetric.timestampMs)
	}

	// Create EMF metrics if there are measurements
	// https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch_Embedded_Metric_Format_Specification.html#CloudWatch_Embedded_Metric_Format_Specification_structure
	if len(cWMetric.measurements) > 0 && !config.DisableMetricExtraction {
		if config.Version == "1" {
			/* 	EMF V1
				"Version": "1",
				"_aws": {
					"CloudWatchMetrics": [
					{
						"Namespace": "ECS",
						"Dimensions": [ ["ClusterName"] ],
						"Metrics": [{"Name": "memcached_commands_total"}]
					}
					],
					"Timestamp": 1668387032641
			  	}
			*/
			fieldMap["Version"] = "1"
			fieldMap["_aws"] = map[string]any{
				"CloudWatchMetrics": cWMetric.measurements,
				"Timestamp":         cWMetric.timestampMs,
			}

		} else {
			/* 	EMF V0
				{
					"Version": "0",
					"CloudWatchMetrics": [
					{
						"Namespace": "ECS",
						"Dimensions": [ ["ClusterName"] ],
						"Metrics": [{"Name": "memcached_commands_total"}]
					}
					],
					"Timestamp": "1668387032641"
			  	}
			*/
			fieldMap["CloudWatchMetrics"] = cWMetric.measurements
		}
	} else if len(cWMetric.measurements) < 1 && config.EnhancedContainerInsights {
		// Return nil if requests does not contain metrics when EnhancedContainerInsights is enabled
		return nil, nil
	}

	// remove metrics from fieldMap
	metricsMap := make(map[string]any)
	for _, measurement := range cWMetric.measurements {
		for _, metric := range measurement.Metrics {
			metricName, exist := metric["Name"]
			if exist {
				v, ok := fieldMap[metricName]
				if ok {
					metricsMap[metricName] = v
					delete(fieldMap, metricName)
				}
			}
		}
	}

	pleMsg, err := json.Marshal(fieldMap)
	if err != nil {
		return nil, err
	}

	// append metrics json to pleMsg
	if len(metricsMap) > 0 {
		metricsMsg, err := json.Marshal(metricsMap)
		if err != nil {
			return nil, err
		}
		metricsMsg[0] = ','
		pleMsg = append(pleMsg[:len(pleMsg)-1], metricsMsg...)
	}

	metricCreationTime := cWMetric.timestampMs
	logEvent := cwlogs.NewEvent(
		metricCreationTime,
		string(pleMsg),
	)
	logEvent.GeneratedTime = time.Unix(0, metricCreationTime*int64(time.Millisecond))

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
