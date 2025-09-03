# EscapedCaptureGroupOne Constant Usage Example

The `EscapedCaptureGroupOne` constant provides a safe way to reference regex capture groups in Prometheus receiver configurations without using the raw `$1` syntax that can cause YAML parsing issues.

## Usage

Instead of using `$1` directly in your configuration:

```yaml
# This can cause YAML parsing issues
scrape_configs:
  - job_name: kubernetes-pods
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_name]
        regex: '(.+)'
        target_label: pod
        replacement: $1  # This might cause issues during OTEL yaml validation
```

Use the exported constant:

```go
import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"

config := map[string]any{
    "scrape_configs": []any{
        map[string]any{
            "job_name": "kubernetes-pods",
            "relabel_configs": []any{
                map[string]any{
                    "source_labels": []any{"__meta_kubernetes_pod_name"},
                    "regex":         "(.+)",
                    "target_label":  "pod",
                    "replacement":   prometheusreceiver.EscapedCaptureGroupOne, // Safe to use
                },
            },
        },
    },
}
```

## How it works

1. The constant `EscapedCaptureGroupOne` has the value `"__capture_group_1__"`
2. During configuration preprocessing, this constant is automatically replaced with `"$1"`
3. This ensures proper YAML parsing while maintaining the intended regex functionality

## Benefits

- **Type Safety**: The constant is exported and can be referenced safely in Go code
- **YAML Compatibility**: Avoids issues with `$` characters in YAML parsing
- **Maintainability**: Clear intent and easier to refactor if needed
- **Testing**: Can be easily tested and validated
