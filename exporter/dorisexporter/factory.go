// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dorisexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/dorisexporter"

import (
	"context"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/dorisexporter/internal/metadata"
)

func NewFactory() exporter.Factory {
	return exporter.NewFactory(
		metadata.Type,
		createDefaultConfig,
		exporter.WithLogs(createLogsExporter, metadata.LogsStability),
		exporter.WithTraces(createTracesExporter, metadata.TracesStability),
		exporter.WithMetrics(createMetricsExporter, metadata.MetricsStability),
	)
}

func createDefaultConfig() component.Config {
	httpClientConfig := confighttp.NewDefaultClientConfig()
	httpClientConfig.Timeout = 60 * time.Second

	return &Config{
		ClientConfig:  httpClientConfig,
		QueueSettings: exporterhelper.NewDefaultQueueSettings(),
		BackOffConfig: configretry.NewDefaultBackOffConfig(),
		Table: Table{
			Logs:    "otel_logs",
			Traces:  "otel_traces",
			Metrics: "otel_metrics",
		},
		Database:          "otel",
		CreateSchema:      true,
		HistoryDays:       0,
		CreateHistoryDays: 0,
		ReplicationNum:    1,
		TimeZone:          time.Local.String(),
	}
}

func createLogsExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
	return exporterhelper.NewLogsExporter(
		ctx,
		set,
		cfg,
		func(_ context.Context, _ plog.Logs) error {
			return nil
		},
	)
}

func createTracesExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
	c := cfg.(*Config)
	exporter := newTracesExporter(set.Logger, c, set.TelemetrySettings)
	return exporterhelper.NewTracesExporter(
		ctx,
		set,
		cfg,
		exporter.pushTraceData,
		exporterhelper.WithStart(exporter.start),
		exporterhelper.WithShutdown(exporter.shutdown),
		// we config the timeout option in http client, so we don't need to set timeout here
		exporterhelper.WithTimeout(exporterhelper.TimeoutSettings{Timeout: 0}),
		exporterhelper.WithQueue(c.QueueSettings),
		exporterhelper.WithRetry(c.BackOffConfig),
	)
}

func createMetricsExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
	return exporterhelper.NewMetricsExporter(
		ctx,
		set,
		cfg,
		func(_ context.Context, _ pmetric.Metrics) error {
			return nil
		},
	)
}