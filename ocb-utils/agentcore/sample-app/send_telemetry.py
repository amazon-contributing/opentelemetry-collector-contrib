#!/usr/bin/env python3
"""Simple Python app to send OpenTelemetry spans, metrics, and logs to collector."""

import time
import logging

from opentelemetry import trace, metrics
from opentelemetry.sdk.resources import Resource
# Traces
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
# Metrics
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
# Logs
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter

ENDPOINT = "http://localhost:4318"

resource = Resource.create({
    "service.name": "sample-test-app",
    "service.version": "1.0.0",
    "deployment.environment": "local-test",
})

# ── Traces ──
trace_provider = TracerProvider(resource=resource)
trace_provider.add_span_processor(
    BatchSpanProcessor(OTLPSpanExporter(endpoint=f"{ENDPOINT}/v1/traces"))
)
trace.set_tracer_provider(trace_provider)
tracer = trace.get_tracer("sample-tracer")

# ── Metrics ──
metric_reader = PeriodicExportingMetricReader(
    OTLPMetricExporter(endpoint=f"{ENDPOINT}/v1/metrics"),
    export_interval_millis=5000,
)
meter_provider = MeterProvider(resource=resource, metric_readers=[metric_reader])
metrics.set_meter_provider(meter_provider)
meter = metrics.get_meter("sample-meter")

# ── Logs ──
log_provider = LoggerProvider(resource=resource)
log_provider.add_log_record_processor(
    BatchLogRecordProcessor(OTLPLogExporter(endpoint=f"{ENDPOINT}/v1/logs"))
)
handler = LoggingHandler(level=logging.DEBUG, logger_provider=log_provider)
logger = logging.getLogger("sample-app")
logger.setLevel(logging.DEBUG)
logger.addHandler(handler)

# ── Create instruments ──
request_counter = meter.create_counter(
    name="app.request.count",
    description="Total number of requests",
    unit="1",
)
request_duration = meter.create_histogram(
    name="app.request.duration",
    description="Request duration in milliseconds",
    unit="ms",
)
active_users = meter.create_up_down_counter(
    name="app.active_users",
    description="Number of active users",
    unit="1",
)

# ── Send telemetry ──
print("Sending telemetry data (traces, metrics, logs)...")

for i in range(3):
    # Traces: create parent + child spans
    with tracer.start_as_current_span("http-request", attributes={
        "http.method": "GET",
        "http.url": f"/api/items/{i}",
        "http.status_code": 200,
    }) as parent:
        time.sleep(0.05)

        with tracer.start_as_current_span("db-query", attributes={
            "db.system": "mysql",
            "db.statement": f"SELECT * FROM items WHERE id={i}",
        }):
            time.sleep(0.03)

    # Metrics
    request_counter.add(1, {"http.method": "GET", "http.route": "/api/items"})
    request_duration.record(80 + i * 10, {"http.method": "GET", "http.route": "/api/items"})
    active_users.add(1)

    # Logs
    logger.info("Processed request %d for /api/items/%d", i + 1, i)

    print(f"  Iteration {i + 1}/3 done")
    time.sleep(1)

# Send some warning/error logs
logger.warning("High latency detected on /api/items")
logger.error("Failed to connect to cache server", extra={"cache.host": "redis-01"})

# Wait for export
print("Waiting for export flush...")
time.sleep(6)

trace_provider.shutdown()
meter_provider.shutdown()
log_provider.shutdown()

print("Done! Check collector logs for traces, metrics, and logs.")
