# AWS CloudWatch Logs Provisioner Extension

The AWS CloudWatch Logs Provisioner extension creates CloudWatch log groups and streams on first encounter. It implements [`extensionauth.HTTPClient`](https://pkg.go.dev/go.opentelemetry.io/collector/extension/extensionauth#HTTPClient) to participate in the HTTP auth chain.

This extension is designed for use with the `otlphttp` exporter to send logs to the [CloudWatch OTLP endpoint](https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-OTLPEndpoint.html), which requires pre-existing log groups and the `x-aws-log-group` header on every request.

## How it works

1. Reads `x-aws-log-group` and `x-aws-log-stream` headers from the outgoing HTTP request (set by the otlphttp exporter or [headers_setter](../../extension/headerssetterextension) extension)
2. Creates the log group and stream via the AWS CloudWatch Logs API if not already cached
3. Delegates to the inner auth extension (e.g., `sigv4auth`) for request signing

## Configuration

| Field | Default | Description |
|---|---|---|
| `region` | (required) | AWS region for CloudWatch Logs API calls |
| `additional_auth` | (none) | Inner auth extension for request signing (typically `sigv4auth`) |
| `logs_provision_timeout` | `10s` | HTTP timeout for each CreateLogGroup/CreateLogStream API call |
| `logs_provision_failure_backoff` | `30s` | TTL for negative cache entries after a creation failure |

## Examples

### Dynamic routing (per-service log groups)

Routes OTLP logs to per-service CloudWatch log groups based on the `service.name` resource attribute. The `transform` processor builds the full log group name, the `attributestocontext` processor copies it to `client.Metadata`, and the `headers_setter` extension sets the `x-aws-log-group` header from metadata.

```yaml
extensions:
  sigv4auth/logs:
    region: us-east-1
    service: logs

  awscloudwatchlogsprovisioner:
    region: us-east-1
    additional_auth: sigv4auth/logs

  headers_setter:
    additional_auth: awscloudwatchlogsprovisioner
    headers:
      - key: x-aws-log-group
        from_context: cwlogs.log_group
      - key: x-aws-log-stream
        from_context: cwlogs.log_stream

receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  transform:
    log_statements:
      - context: resource
        statements:
          - set(resource.attributes["cwlogs.log_group"], Concat(["/aws/telemetry/", resource.attributes["service.name"]], ""))
          - set(resource.attributes["cwlogs.log_stream"], "default")

  attributestocontext:
    actions:
      - key: cwlogs.log_group
        from_resource_attribute: cwlogs.log_group
      - key: cwlogs.log_stream
        from_resource_attribute: cwlogs.log_stream

  batch:
    metadata_keys:
      - cwlogs.log_group
      - cwlogs.log_stream

exporters:
  otlphttp/cw-logs:
    endpoint: https://logs.us-east-1.amazonaws.com
    logs_endpoint: https://logs.us-east-1.amazonaws.com/v1/logs
    auth:
      authenticator: headers_setter
    compression: gzip

service:
  extensions: [sigv4auth/logs, awscloudwatchlogsprovisioner, headers_setter]
  pipelines:
    logs:
      receivers: [otlp]
      processors: [transform, attributestocontext, batch]
      exporters: [otlphttp/cw-logs]
```

### Static routing (single log group)

All logs go to a single, pre-defined log group. No `transform` or `attributestocontext` processors needed — the otlphttp exporter sets the headers directly.

```yaml
extensions:
  sigv4auth/logs:
    region: us-east-1
    service: logs

  awscloudwatchlogsprovisioner:
    region: us-east-1
    additional_auth: sigv4auth/logs

receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317

processors:
  batch:

exporters:
  otlphttp/cw-logs:
    endpoint: https://logs.us-east-1.amazonaws.com
    logs_endpoint: https://logs.us-east-1.amazonaws.com/v1/logs
    headers:
      x-aws-log-group: /my-app/logs
      x-aws-log-stream: default
    auth:
      authenticator: awscloudwatchlogsprovisioner
    compression: gzip

service:
  extensions: [sigv4auth/logs, awscloudwatchlogsprovisioner]
  pipelines:
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlphttp/cw-logs]
```

## Provisioning behavior

- **Singleflight**: Only one API call per (log group, stream) pair. Concurrent requests for the same key block until the first goroutine completes.
- **Negative cache**: Failed creation attempts are cached for `logs_provision_failure_backoff`. During this period, the extension skips retries for that key.
- **Cache eviction**: If the CW OTLP endpoint returns 400 with "does not exist" and the cache had a success entry, the entry is evicted and a retryable error is returned — the exporter retries, re-provisions, and delivers the logs.
- **AlreadyExists**: `ResourceAlreadyExistsException` from the API is treated as success.
