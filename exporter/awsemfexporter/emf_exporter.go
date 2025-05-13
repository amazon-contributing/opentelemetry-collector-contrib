// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsemfexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter"

import (
	"context"
	"errors"
	"fmt"
	"log"
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

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter/internal/appsignals"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsemfexporter/internal/metadata"
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
	}

	if config.IsAppSignalsEnabled() {
		userAgent := appsignals.NewUserAgent()
		emfExporter.processResourceLabels = userAgent.Process
	}

	config.logger.Warn("the default value for DimensionRollupOption will be changing to NoDimensionRollup" +
		"in a future release. See https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/23997 for more" +
		"information")

	return emfExporter, nil
}

func (emf *emfExporter) pushMetricsData(_ context.Context, md pmetric.Metrics) error {
	log.Println("\n=== START: pushMetricsData ===")
	log.Println("Initial metrics count:", md.ResourceMetrics().Len())

	if emf.config.logger != nil {
		log.Println("Logger is configured")
	} else {
		log.Println("WARNING: Logger is nil")
	}

	rms := md.ResourceMetrics()
	labels := map[string]string{}
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		am := rm.Resource().Attributes()
		if am.Len() > 0 {
			for k, v := range am.All() {
				labels[k] = v.Str()
			}
		}
	}

	log.Println("\n=== Resource Labels ===")
	log.Printf("Labels found: %+v\n", labels)
	emf.processResourceLabels(labels)
	log.Println("After processing labels:", labels)

	groupedMetrics := make(map[any]*groupedMetric)
	defaultLogStream := fmt.Sprintf("otel-stream-%s", emf.collectorID)
	outputDestination := emf.config.OutputDestination

	log.Println("\n=== Configuration ===")
	log.Println("Output Destination:", outputDestination)
	log.Println("Default Log Stream:", defaultLogStream)
	log.Println("Initial Grouped Metrics length:", len(groupedMetrics))

	log.Println("\n=== Starting Metric Translation ===")
	for i := 0; i < rms.Len(); i++ {
		log.Printf("Processing resource metric %d of %d\n", i+1, rms.Len())
		err := emf.metricTranslator.translateOTelToGroupedMetric(rms.At(i), groupedMetrics, emf.config)
		if err != nil {
			log.Printf("ERROR during translation: %v\n", err)
			return err
		}
	}

	log.Println("\n=== After Translation ===")
	log.Printf("Grouped Metrics count: %d\n", len(groupedMetrics))
	for key, metric := range groupedMetrics {
		log.Printf("Metric Key: %v\n", key)
		log.Printf("Metric Value: %+v\n", metric)
	}

	log.Println("\n=== Processing Grouped Metrics ===")
	for _, groupedMetric := range groupedMetrics {
		log.Println("Processing new grouped metric")

		putLogEvent, err := translateGroupedMetricToEmf(groupedMetric, emf.config, defaultLogStream)
		if err != nil {
			if errors.Is(err, errMissingMetricsForEnhancedContainerInsights) {
				log.Println("Dropping empty putLogEvents for enhanced container insights:", err)
				continue
			}
			log.Printf("ERROR translating to EMF: %v\n", err)
			return err
		}

		log.Println("Successfully translated to EMF format")

		if strings.EqualFold(outputDestination, outputDestinationStdout) {
			log.Println("\n=== Writing to Stdout ===")
			if putLogEvent != nil && putLogEvent.InputLogEvent != nil && putLogEvent.InputLogEvent.Message != nil {
				log.Printf("Log Event Message: %s\n", *putLogEvent.InputLogEvent.Message)
			}
		} else if strings.EqualFold(outputDestination, outputDestinationCloudWatch) {
			log.Println("\n=== Writing to CloudWatch ===")
			log.Printf("Stream Key: %+v\n", putLogEvent.StreamKey)

			emfPusher, err := emf.getPusher(putLogEvent.StreamKey)
			if err != nil {
				log.Printf("ERROR getting pusher: %v\n", err)
				return fmt.Errorf("failed to get pusher: %w", err)
			}

			if emfPusher != nil {
				log.Println("Got pusher, adding log entry")
				returnError := emfPusher.AddLogEntry(putLogEvent)
				if returnError != nil {
					log.Printf("ERROR adding log entry: %v\n", returnError)
					return wrapErrorIfBadRequest(returnError)
				}
				log.Println("Successfully added log entry")
			}
		}
	}

	if strings.EqualFold(outputDestination, outputDestinationCloudWatch) {
		log.Println("\n=== Force Flushing Pushers ===")
		pushers := emf.listPushers()
		log.Printf("Number of pushers to flush: %d\n", len(pushers))

		for i, emfPusher := range pushers {
			log.Printf("Flushing pusher %d\n", i+1)
			returnError := emfPusher.ForceFlush()
			if returnError != nil {
				err := wrapErrorIfBadRequest(returnError)
				if err != nil {
					log.Printf("ERROR flushing pusher %d: %v\n", i+1, err)
				}
				return err
			}
			log.Printf("Successfully flushed pusher %d\n", i+1)
		}
	}

	log.Println("\n=== END: pushMetricsData ===")
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
