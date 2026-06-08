// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8staintsprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8staintsprocessor"

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/k8sconfig"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8staintsprocessor/internal/metadata"
)

var processorCapabilities = consumer.Capabilities{MutatesData: true}

// NewFactory creates a new factory for the k8staints processor.
func NewFactory() processor.Factory {
	var (
		once       sync.Once
		sharedProc *k8sTaintsProcessor
	)
	getShared := func(cfg *Config, set processor.Settings) *k8sTaintsProcessor {
		once.Do(func() { sharedProc = newProcessor(cfg, set.Logger) })
		return sharedProc
	}

	return processor.NewFactory(
		metadata.Type,
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessorFunc(getShared), metadata.MetricsStability),
		processor.WithLogs(createLogsProcessorFunc(getShared), metadata.LogsStability),
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		APIConfig: k8sconfig.APIConfig{AuthType: k8sconfig.AuthTypeServiceAccount},
	}
}

type sharedProcessorGetter func(*Config, processor.Settings) *k8sTaintsProcessor

func createMetricsProcessorFunc(getShared sharedProcessorGetter) processor.CreateMetricsFunc {
	return func(
		ctx context.Context,
		set processor.Settings,
		cfg component.Config,
		nextConsumer consumer.Metrics,
	) (processor.Metrics, error) {
		processorCfg, ok := cfg.(*Config)
		if !ok {
			return nil, fmt.Errorf("invalid configuration type: %T", cfg)
		}

		p := getShared(processorCfg, set)

		return processorhelper.NewMetrics(
			ctx, set, cfg, nextConsumer,
			p.processMetrics,
			processorhelper.WithStart(p.Start),
			processorhelper.WithShutdown(p.Shutdown),
			processorhelper.WithCapabilities(processorCapabilities),
		)
	}
}

func createLogsProcessorFunc(getShared sharedProcessorGetter) processor.CreateLogsFunc {
	return func(
		ctx context.Context,
		set processor.Settings,
		cfg component.Config,
		nextConsumer consumer.Logs,
	) (processor.Logs, error) {
		processorCfg, ok := cfg.(*Config)
		if !ok {
			return nil, fmt.Errorf("invalid configuration type: %T", cfg)
		}

		p := getShared(processorCfg, set)

		return processorhelper.NewLogs(
			ctx, set, cfg, nextConsumer,
			p.processLogs,
			processorhelper.WithStart(p.Start),
			processorhelper.WithShutdown(p.Shutdown),
			processorhelper.WithCapabilities(processorCapabilities),
		)
	}
}
