// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsdeviceslurmjobcorrelationprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor"

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/awsdeviceslurmjobcorrelationprocessor/internal/metadata"
)

var processorCapabilities = consumer.Capabilities{MutatesData: true}

func NewFactory() processor.Factory {
	return processor.NewFactory(
		metadata.Type,
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, metadata.MetricsStability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		CgroupRoot:           "/sys/fs/cgroup",
		CgroupVersion:        "v1",
		MetadataPollInterval: 30_000_000_000, // 30s in nanoseconds
		NodeExclusive:        true,
		DeviceTypes: []DeviceTypeConfig{
			{
				Name:              "gpu",
				DeviceIDAttribute: "gpu",
				DeviceIDSource:    DeviceIDSourceDatapoint,
			},
			{
				Name:              "efa",
				DeviceIDAttribute: "aws.efa.device",
				DeviceIDSource:    DeviceIDSourceResource,
			},
		},
	}
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

	processorCfg.setDefaults()
	metricsProcessor := newProcessor(processorCfg, set.Logger)

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
