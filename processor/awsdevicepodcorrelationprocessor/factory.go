// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdevicepodcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor"

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdevicepodcorrelationprocessor/internal/metadata"
)

var (
	processorCapabilities = consumer.Capabilities{MutatesData: true}

	// DefaultStoreFactory is the factory function for creating the PodResourcesStore.
	// It must be set by the host application (e.g., CW Agent) during component
	// registration, before the processor is started. This indirection is needed
	// because the real PodResourcesStore lives in an internal package that cannot
	// be imported across module boundaries.
	DefaultStoreFactory PodResourcesStoreFactory
)

// NewFactory creates a new factory for the awsdevicepodcorrelation processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		metadata.Type,
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, metadata.MetricsStability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createMetricsProcessor(
	ctx context.Context,
	set processor.Settings,
	cfg component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	processorCfg, ok := cfg.(*Config)
	if !ok {
		return nil, fmt.Errorf("invalid configuration type: %T", cfg)
	}

	if DefaultStoreFactory == nil {
		return nil, fmt.Errorf("PodResourcesStore factory not configured; the host application must set awsdevicepodcorrelationprocessor.DefaultStoreFactory before using this processor")
	}

	metricsProcessor := newProcessor(processorCfg, set.Logger, DefaultStoreFactory)

	return processorhelper.NewMetrics(
		ctx,
		set,
		cfg,
		nextConsumer,
		metricsProcessor.processMetrics,
		processorhelper.WithStart(metricsProcessor.Start),
		processorhelper.WithShutdown(metricsProcessor.Shutdown),
		processorhelper.WithCapabilities(processorCapabilities),
	)
}
