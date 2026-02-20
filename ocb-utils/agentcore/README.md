# OpenTelemetry Collector AgentCore Builder

Build OpenTelemetry Collector with Application Signals components using current branch code.

## Prerequisites

- **Docker**: Must be installed and running (the build runs inside a Docker container)
- **Go**: Required for auto-detecting host platform (`go env GOOS`/`go env GOARCH`)

## Quick Start

Build for the current host platform (auto-detected):
```bash
./build-agentcore.sh
```

## Build for Different Platforms

### macOS ARM64 (Apple Silicon)
```bash
GOOS=darwin GOARCH=arm64 ./build-agentcore.sh
```

### macOS AMD64 (Intel)
```bash
GOOS=darwin GOARCH=amd64 ./build-agentcore.sh
```

### Linux ARM64
```bash
GOOS=linux GOARCH=arm64 ./build-agentcore.sh
```

### Linux AMD64
```bash
GOOS=linux GOARCH=amd64 ./build-agentcore.sh
```

### Windows AMD64
```bash
GOOS=windows GOARCH=amd64 ./build-agentcore.sh
```

## Configuration Options

### OCB Version
Specify OpenTelemetry Collector Builder version (default: 0.121.0):
```bash
OCB_VERSION=0.121.0 ./build-agentcore.sh
```

### Combined Example
Build Linux AMD64 with specific OCB version:
```bash
OCB_VERSION=0.121.0 GOOS=linux GOARCH=amd64 ./build-agentcore.sh
```

## Output

Binary will be created at:
```
./output/otelcol-agentcore
```

## Usage

### Environment Variables

The `config.yaml` uses the following environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `AWS_REGION` | AWS Region | `us-west-2` |
| `AWS_APP_LOG_GROUP` | CloudWatch Log Group for application logs (otlphttp/logs) | `AgentCoreAppLogs` |
| `AWS_APP_LOG_STREAM` | CloudWatch Log Stream for application logs (otlphttp/logs) | `default` |
| `AWS_EMF_NAMESPACE` | CloudWatch Metrics Namespace for EMF exporter | `AgentCoreMetrics` |
| `AWS_EMF_LOG_GROUP` | CloudWatch Log Group for EMF metrics | `AgentCoreEMF` |
| `AWS_EMF_LOG_STREAM` | CloudWatch Log Stream for EMF metrics | `default` |

### Run the Collector

With default values (no environment variables needed):
```bash
./output/otelcol-agentcore --config config.yaml
```

With custom values:
```bash
AWS_REGION=us-east-1 \
AWS_APP_LOG_GROUP=MyAppLogs \
AWS_EMF_NAMESPACE=MyMetrics \
  ./output/otelcol-agentcore --config config.yaml
```

### View Available Components

```bash
./output/otelcol-agentcore components
```

## Pipelines

The default `config.yaml` includes the following pipelines:

| Pipeline | Receivers | Processors | Exporters |
|----------|-----------|------------|-----------|
| **traces** | otlp | batch | debug, otlphttp/traces (X-Ray) |
| **logs** | otlp | batch | debug, otlphttp/logs (CloudWatch Logs) |
| **metrics** | otlp | batch | debug, awsemf (CloudWatch Metrics) |

## Supported Platforms

- **GOOS**: `darwin`, `linux`, `windows`
- **GOARCH**: `amd64`, `arm64`, `386`, `arm`

## Components

The collector includes:
- **Processors**: Attributes, Filter, Metrics Transform, Batch
- **Exporters**: AWS EMF, OTLP HTTP, Debug
- **Receivers**: OTLP
- **Extensions**: AWS Proxy, SigV4 Auth

All AWS Application Signals components use local code from current branch.

## E2E Testing

Build and test in two steps:
```bash
./build-agentcore.sh
./test-agentcore.sh
```

### Prerequisites

- Docker running (for the build step)
- Go (for platform auto-detection)
- Python 3 (venv and pip dependencies are set up automatically by the test script)
- Valid AWS credentials (env vars or `~/.aws/credentials`)

### What the Test Verifies

1. Collector starts and becomes ready
2. Traces, metrics, and logs are sent via OTLP
3. Data arrives in CloudWatch (filtered by test start time to avoid false positives)
4. No export errors in collector logs

| Signal | CloudWatch Log Group | Log Stream |
|--------|---------------------|------------|
| Logs | `AgentCoreAppLogs` | `default` |
| Metrics (EMF) | `AgentCoreEMF` | `default` |
| Traces (Spans) | `aws/spans` | `default` |