// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter"

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/amazon-contributing/opentelemetry-collector-contrib/extension/awsmiddleware"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/google/uuid"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter/internal/useragent"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/awsutil"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/cwlogs"
)

const (
	// OutputDestination Options
	outputDestinationCloudWatch = "cloudwatch"
	outputDestinationStdout     = "stdout"

	// AppSignals EMF config
	appSignalsMetricNamespace    = "ApplicationSignals"
	appSignalsLogGroupNamePrefix = "/aws/application-signals/"
)

var enhancedContainerInsightsEKSPattern = regexp.MustCompile(`^/aws/containerinsights/\S+/performance$`)

type emfExporter struct {
	pusherMap        map[cwlogs.StreamKey]cwlogs.Pusher
	svcStructuredLog *cwlogs.Client
	config           *Config
	set              exporter.Settings

	metricTranslator metricTranslator

	pusherMapLock sync.Mutex
	retryCnt      int
	collectorID   string

	processResourceLabels func(map[string]string)
	processMetrics        func(pmetric.Metrics)
}

// newEmfExporter creates a new exporter using exporterhelper
func newEmfExporter(config *Config, set exporter.Settings) (*emfExporter, error) {
	if config == nil {
		return nil, errors.New("emf exporter config is nil")
	}

	config.logger = set.Logger

	collectorIdentifier, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}

	// Initialize emfExporter without AWS session and structured logs
	emfExporter := &emfExporter{
		config:                config,
		metricTranslator:      newMetricTranslator(*config),
		retryCnt:              config.MaxRetries,
		collectorID:           collectorIdentifier.String(),
		pusherMap:             map[cwlogs.StreamKey]cwlogs.Pusher{},
		processResourceLabels: func(map[string]string) {},
		processMetrics:        func(pmetric.Metrics) {},
	}

	config.logger.Warn("the default value for DimensionRollupOption will be changing to NoDimensionRollup" +
		"in a future release. See https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/23997 for more" +
		"information")

	return emfExporter, nil
}

func (emf *emfExporter) pushMetricsData(_ context.Context, md pmetric.Metrics) error {
    rms := md.ResourceMetrics()
    labels := map[string]string{}
    
    emf.config.logger.Debug("Starting pushMetricsData",
        zap.Int("total_resource_metrics", rms.Len()))

    // Process resource labels
    for i := 0; i < rms.Len(); i++ {
        rm := rms.At(i)
        am := rm.Resource().Attributes()
        if am.Len() > 0 {
            emf.config.logger.Debug("Processing resource attributes",
                zap.Int("attribute_count", am.Len()),
                zap.Int("resource_index", i))
            for k, v := range am.All() {
                labels[k] = v.Str()
            }
        }
    }
    
    emf.config.logger.Debug("Resource labels processed", zap.Any("labels", labels))
    emf.processResourceLabels(labels)
    emf.processMetrics(md)

    groupedMetrics := make(map[any]*groupedMetric)
    defaultLogStream := fmt.Sprintf("otel-stream-%s", emf.collectorID)
    outputDestination := emf.config.OutputDestination

    // Translate metrics
    for i := 0; i < rms.Len(); i++ {
        rm := rms.At(i)
        
        // Log metric types being processed
        scopeMetrics := rm.ScopeMetrics()
        for j := 0; j < scopeMetrics.Len(); j++ {
            metrics := scopeMetrics.At(j).Metrics()
            for k := 0; k < metrics.Len(); k++ {
                metric := metrics.At(k)
                emf.config.logger.Debug("Processing metric",
                    zap.String("metric_name", metric.Name()),
                    zap.String("metric_type", metric.Type().String()),
                    zap.Int("resource_index", i),
                    zap.Int("scope_index", j),
                    zap.Int("metric_index", k))

                // Special logging for histograms
                if metric.Type() == pmetric.MetricTypeHistogram {
                    hdp := metric.Histogram().DataPoints()
                    for l := 0; l < hdp.Len(); l++ {
                        dp := hdp.At(l)
                        emf.config.logger.Debug("Histogram details",
                            zap.String("metric_name", metric.Name()),
                            zap.Uint64("count", dp.Count()),
                            zap.Float64("sum", dp.Sum()),
                            zap.Float64("min", dp.Min()),
                            zap.Float64("max", dp.Max()),
                            zap.Any("bucket_counts", dp.BucketCounts().AsRaw()),
                            zap.Any("explicit_bounds", dp.ExplicitBounds().AsRaw()))
                    }
                }
            }
        }

        err := emf.metricTranslator.translateOTelToGroupedMetric(rm, groupedMetrics, emf.config)
        if err != nil {
            emf.config.logger.Error("Failed to translate metrics",
                zap.Error(err),
                zap.Int("resource_index", i))
            return err
        }
    }

    // Process grouped metrics
    emf.config.logger.Debug("Processing grouped metrics",
        zap.Int("group_count", len(groupedMetrics)))

    for key, groupedMetric := range groupedMetrics {
        emf.config.logger.Debug("Processing metric group",
            zap.Any("group_key", key),
            zap.Int("metric_count", len(groupedMetric.metrics)))

        putLogEvent, err := translateGroupedMetricToEmf(groupedMetric, emf.config, defaultLogStream)
        if err != nil {
            if errors.Is(err, errMissingMetricsForEnhancedContainerInsights) {
                emf.config.logger.Debug("Dropping empty putLogEvents for enhanced container insights",
                    zap.Error(err))
                continue
            }
            emf.config.logger.Error("Failed to translate grouped metric to EMF",
                zap.Error(err))
            return err
        }

        // Log EMF output
        if putLogEvent != nil && putLogEvent.InputLogEvent != nil {
            emf.config.logger.Debug("EMF event created",
                zap.Any("stream_key", putLogEvent.StreamKey),  // Changed to zap.Any
                zap.String("message", *putLogEvent.InputLogEvent.Message))
        }

        // Handle output destination
        if strings.EqualFold(outputDestination, outputDestinationStdout) {
            if putLogEvent != nil &&
                putLogEvent.InputLogEvent != nil &&
                putLogEvent.InputLogEvent.Message != nil {
                fmt.Println(*putLogEvent.InputLogEvent.Message)
            }
        } else if strings.EqualFold(outputDestination, outputDestinationCloudWatch) {
            emfPusher, err := emf.getPusher(putLogEvent.StreamKey)
            if err != nil {
                emf.config.logger.Error("Failed to get pusher",
                    zap.Error(err),
                    zap.Any("stream_key", putLogEvent.StreamKey))  // Changed to zap.Any
                return fmt.Errorf("failed to get pusher: %w", err)
            }
            if emfPusher != nil {
                returnError := emfPusher.AddLogEntry(putLogEvent)
                if returnError != nil {
                    emf.config.logger.Error("Failed to add log entry",
                        zap.Error(returnError))
                    return wrapErrorIfBadRequest(returnError)
                }
            }
        }
    }

    // Handle CloudWatch flush
    if strings.EqualFold(outputDestination, outputDestinationCloudWatch) {
        pushers := emf.listPushers()
        emf.config.logger.Debug("Flushing logs to CloudWatch",
            zap.Int("pusher_count", len(pushers)))

        for _, emfPusher := range pushers {
            returnError := emfPusher.ForceFlush()
            if returnError != nil {
                err := wrapErrorIfBadRequest(returnError)
                if err != nil {
                    emf.config.logger.Error("Error force flushing logs. Skipping to next logPusher.",
                        zap.Error(err))
                }
                return err
            }
        }
    }

    emf.config.logger.Debug("Finished processing resource metrics",
        zap.Any("labels", labels))

    return nil
}

func (emf *emfExporter) getPusher(key cwlogs.StreamKey) (cwlogs.Pusher, error) {
	emf.pusherMapLock.Lock()
	defer emf.pusherMapLock.Unlock()

	if emf.svcStructuredLog == nil {
		return nil, errors.New("CloudWatch Logs client not initialized")
	}

	pusher, exists := emf.pusherMap[key]
	if !exists {
		if emf.set.Logger != nil {
			pusher = cwlogs.NewPusher(key, emf.retryCnt, *emf.svcStructuredLog, emf.set.Logger)
		} else {
			pusher = cwlogs.NewPusher(key, emf.retryCnt, *emf.svcStructuredLog, emf.config.logger)
		}
		emf.pusherMap[key] = pusher
	}
	return pusher, nil
}

func (emf *emfExporter) listPushers() []cwlogs.Pusher {
	emf.pusherMapLock.Lock()
	defer emf.pusherMapLock.Unlock()

	var pushers []cwlogs.Pusher
	for _, pusher := range emf.pusherMap {
		pushers = append(pushers, pusher)
	}
	return pushers
}

func (emf *emfExporter) start(_ context.Context, host component.Host) error {
	// Create AWS session here
	awsConfig, session, err := awsutil.GetAWSConfigSession(emf.config.logger, &awsutil.Conn{}, &emf.config.AWSSessionSettings)
	if err != nil {
		return err
	}

	var userAgentExtras []string
	if emf.config.IsAppSignalsEnabled() {
		userAgentExtras = append(userAgentExtras, "AppSignals")
	}
	if emf.config.IsEnhancedContainerInsights() && enhancedContainerInsightsEKSPattern.MatchString(emf.config.LogGroupName) {
		userAgentExtras = append(userAgentExtras, "EnhancedEKSContainerInsights")
	}

	// create CWLogs client with aws session config
	svcStructuredLog := cwlogs.NewClient(emf.config.logger,
		awsConfig,
		emf.set.BuildInfo,
		emf.config.LogGroupName,
		emf.config.LogRetention,
		emf.config.Tags,
		session,
		metadata.Type.String(),
		cwlogs.WithUserAgentExtras(userAgentExtras...),
	)

	// Assign to the struct
	emf.svcStructuredLog = svcStructuredLog

	// Optionally configure middleware
	if emf.config.MiddlewareID != nil {
		awsmiddleware.TryConfigure(emf.config.logger, host, *emf.config.MiddlewareID, awsmiddleware.SDKv1(svcStructuredLog.Handlers()))
	}

	// Below are optimizatons to minimize amoount of
	// metrics processing. We have two scearios
	// 1. AppSignal - Only run Process function for AppSignal related useragent
	// 2. Enhanced Container Insights - Only run ProcessMetrics function for CI EBS related useragent
	if emf.config.IsAppSignalsEnabled() || emf.config.IsEnhancedContainerInsights() {
		userAgent := useragent.NewUserAgent()
		emf.svcStructuredLog.Handlers().Build.PushFrontNamed(userAgent.Handler())
		if emf.config.IsAppSignalsEnabled() {
			emf.processResourceLabels = userAgent.Process
		}
		if emf.config.IsEnhancedContainerInsights() {
			emf.processMetrics = userAgent.ProcessMetrics
		}
	}

	return nil
}

// shutdown stops the exporter and is invoked during shutdown.
func (emf *emfExporter) shutdown(_ context.Context) error {
	for _, emfPusher := range emf.listPushers() {
		returnError := emfPusher.ForceFlush()
		if returnError != nil {
			err := wrapErrorIfBadRequest(returnError)
			if err != nil {
				emf.config.logger.Error("Error when gracefully shutting down emf_exporter. Skipping to next logPusher.", zap.Error(err))
			}
		}
	}

	return emf.metricTranslator.Shutdown()
}

func wrapErrorIfBadRequest(err error) error {
	var rfErr awserr.RequestFailure
	if errors.As(err, &rfErr) && rfErr.StatusCode() < 500 {
		return consumererror.NewPermanent(err)
	}
	return err
}
