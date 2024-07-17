// Code generated by mdatagen. DO NOT EDIT.

package metadata

import (
	"errors"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtelemetry"
)

func Meter(settings component.TelemetrySettings) metric.Meter {
	return settings.MeterProvider.Meter("otelcol/fluentforwardreceiver")
}

func Tracer(settings component.TelemetrySettings) trace.Tracer {
	return settings.TracerProvider.Tracer("otelcol/fluentforwardreceiver")
}

// TelemetryBuilder provides an interface for components to report telemetry
// as defined in metadata and user config.
type TelemetryBuilder struct {
	meter                   metric.Meter
	FluentClosedConnections metric.Int64UpDownCounter
	FluentEventsParsed      metric.Int64UpDownCounter
	FluentOpenedConnections metric.Int64UpDownCounter
	FluentParseFailures     metric.Int64UpDownCounter
	FluentRecordsGenerated  metric.Int64UpDownCounter
	level                   configtelemetry.Level
}

// telemetryBuilderOption applies changes to default builder.
type telemetryBuilderOption func(*TelemetryBuilder)

// WithLevel sets the current telemetry level for the component.
func WithLevel(lvl configtelemetry.Level) telemetryBuilderOption {
	return func(builder *TelemetryBuilder) {
		builder.level = lvl
	}
}

// NewTelemetryBuilder provides a struct with methods to update all internal telemetry
// for a component
func NewTelemetryBuilder(settings component.TelemetrySettings, options ...telemetryBuilderOption) (*TelemetryBuilder, error) {
	builder := TelemetryBuilder{level: configtelemetry.LevelBasic}
	for _, op := range options {
		op(&builder)
	}
	var err, errs error
	if builder.level >= configtelemetry.LevelBasic {
		builder.meter = Meter(settings)
	} else {
		builder.meter = noop.Meter{}
	}
	builder.FluentClosedConnections, err = builder.meter.Int64UpDownCounter(
		"fluent_closed_connections",
		metric.WithDescription("Number of connections closed to the fluentforward receiver"),
		metric.WithUnit("1"),
	)
	errs = errors.Join(errs, err)
	builder.FluentEventsParsed, err = builder.meter.Int64UpDownCounter(
		"fluent_events_parsed",
		metric.WithDescription("Number of Fluent events parsed successfully"),
		metric.WithUnit("1"),
	)
	errs = errors.Join(errs, err)
	builder.FluentOpenedConnections, err = builder.meter.Int64UpDownCounter(
		"fluent_opened_connections",
		metric.WithDescription("Number of connections opened to the fluentforward receiver"),
		metric.WithUnit("1"),
	)
	errs = errors.Join(errs, err)
	builder.FluentParseFailures, err = builder.meter.Int64UpDownCounter(
		"fluent_parse_failures",
		metric.WithDescription("Number of times Fluent messages failed to be decoded"),
		metric.WithUnit("1"),
	)
	errs = errors.Join(errs, err)
	builder.FluentRecordsGenerated, err = builder.meter.Int64UpDownCounter(
		"fluent_records_generated",
		metric.WithDescription("Number of log records generated from Fluent forward input"),
		metric.WithUnit("1"),
	)
	errs = errors.Join(errs, err)
	return &builder, errs
}
