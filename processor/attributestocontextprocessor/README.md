# Attributes to Context Processor

The Attributes to Context processor copies resource attributes into [client.Metadata](https://pkg.go.dev/go.opentelemetry.io/collector/client#Metadata) stored in the context. This makes resource attributes available to downstream extensions that read from the request context, such as [Headers Setter](../../extension/headerssetterextension) (via `from_context`).

## Configuration

```yaml
processors:
  attributestocontext:
    actions:
      - key: "cwlogs.log_group"
        from_resource_attribute: "cwlogs.log_group"
      - key: "cwlogs.log_stream"
        from_resource_attribute: "cwlogs.log_stream"
```

### Configuration Options

- `actions`: List of resource attributes to copy to client metadata (required, non-empty)
  - `key`: The key to use in the client metadata (required)
  - `from_resource_attribute`: The resource attribute to read the value from (required)

Each action performs an upsert: it sets the metadata key to the resource attribute value, overwriting any existing value for that key. If the resource attribute is missing, the metadata key is left unchanged. When a batch contains multiple resource entries, the last value wins.

Existing metadata from upstream processors is preserved for keys not listed in `actions`.

## Example: Dynamic log group routing

This example routes OTLP logs to per-service CloudWatch log groups. The `transform` processor builds the log group name from `service.name`, `attributestocontext` copies it to `client.Metadata`, and the `headers_setter` extension sets the HTTP header from metadata.

```yaml
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

extensions:
  sigv4auth/logs:
    region: us-east-1
    service: logs

  headers_setter:
    additional_auth: sigv4auth/logs
    headers:
      - key: x-aws-log-group
        from_context: cwlogs.log_group
      - key: x-aws-log-stream
        from_context: cwlogs.log_stream

exporters:
  otlphttp/cw-logs:
    endpoint: https://logs.us-east-1.amazonaws.com
    logs_endpoint: https://logs.us-east-1.amazonaws.com/v1/logs
    auth:
      authenticator: headers_setter
    compression: gzip

service:
  extensions: [sigv4auth/logs, headers_setter]
  pipelines:
    logs:
      receivers: [otlp]
      processors: [transform, attributestocontext, batch]
      exporters: [otlphttp/cw-logs]
```
