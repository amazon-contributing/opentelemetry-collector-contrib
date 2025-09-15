module github.com/amazon-contributing/opentelemetry-collector-contrib/cmd/generator

go 1.24.7

require (
	github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen v0.135.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.37.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.37.0
	go.opentelemetry.io/otel/metric v1.37.0
	go.opentelemetry.io/otel/sdk/metric v1.37.0
)
