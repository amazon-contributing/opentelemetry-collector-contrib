// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreprocessPrometheusConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name: "simple replacement at top level",
			input: map[string]any{
				"replacement": Escaped_CaptureGroupOne,
				"other_field": "unchanged",
			},
			expected: map[string]any{
				"replacement": "$1",
				"other_field": "unchanged",
			},
		},
		{
			name: "nested map replacement",
			input: map[string]any{
				"relabel_configs": map[string]any{
					"replacement":  Escaped_CaptureGroupOne,
					"target_label": "test",
				},
			},
			expected: map[string]any{
				"relabel_configs": map[string]any{
					"replacement":  "$1",
					"target_label": "test",
				},
			},
		},
		{
			name: "array with map containing replacement",
			input: map[string]any{
				"relabel_configs": []any{
					map[string]any{
						"replacement": Escaped_CaptureGroupOne,
						"action":      "replace",
					},
					map[string]any{
						"replacement": "static_value",
						"action":      "replace",
					},
				},
			},
			expected: map[string]any{
				"relabel_configs": []any{
					map[string]any{
						"replacement": "$1",
						"action":      "replace",
					},
					map[string]any{
						"replacement": "static_value",
						"action":      "replace",
					},
				},
			},
		},
		{
			name: "multiple replacements in array",
			input: map[string]any{
				"scrape_configs": []any{
					map[string]any{
						"relabel_configs": []any{
							map[string]any{
								"replacement": Escaped_CaptureGroupOne,
							},
							map[string]any{
								"replacement": Escaped_CaptureGroupOne,
							},
						},
					},
				},
			},
			expected: map[string]any{
				"scrape_configs": []any{
					map[string]any{
						"relabel_configs": []any{
							map[string]any{
								"replacement": "$1",
							},
							map[string]any{
								"replacement": "$1",
							},
						},
					},
				},
			},
		},
		{
			name: "no replacement needed",
			input: map[string]any{
				"replacement": "static_value",
				"other_field": "unchanged",
			},
			expected: map[string]any{
				"replacement": "static_value",
				"other_field": "unchanged",
			},
		},
		{
			name:     "empty config",
			input:    map[string]any{},
			expected: map[string]any{},
		},
		{
			name: "non-replacement field with constant value",
			input: map[string]any{
				"some_field": Escaped_CaptureGroupOne,
			},
			expected: map[string]any{
				"some_field": Escaped_CaptureGroupOne,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a deep copy to avoid modifying the original
			inputCopy := deepCopyMap(tt.input)
			preprocessPrometheusConfig(inputCopy)
			assert.Equal(t, tt.expected, inputCopy)
		})
	}
}

func TestUnmarshalYAMLWithPreprocessing(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name: "replacement in scrape config",
			input: map[string]any{
				"scrape_configs": []any{
					map[string]any{
						"job_name": "test",
						"relabel_configs": []any{
							map[string]any{
								"source_labels": []any{"__meta_kubernetes_pod_name"},
								"target_label":  "pod",
								"replacement":   Escaped_CaptureGroupOne,
							},
						},
					},
				},
			},
			expected: map[string]any{
				"scrape_configs": []any{
					map[string]any{
						"job_name": "test",
						"relabel_configs": []any{
							map[string]any{
								"source_labels": []any{"__meta_kubernetes_pod_name"},
								"target_label":  "pod",
								"replacement":   "$1",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result map[string]any
			err := unmarshalYAML(tt.input, &result)
			require.NoError(t, err)

			// The preprocessing should have converted the constant
			assert.Equal(t, tt.expected, tt.input)
		})
	}
}

// Helper function to deep copy a map for testing
func deepCopyMap(original map[string]any) map[string]any {
	copy := make(map[string]any)
	for key, value := range original {
		switch v := value.(type) {
		case map[string]any:
			copy[key] = deepCopyMap(v)
		case []any:
			copy[key] = deepCopySlice(v)
		default:
			copy[key] = v
		}
	}
	return copy
}

func deepCopySlice(original []any) []any {
	copy := make([]any, len(original))
	for i, value := range original {
		switch v := value.(type) {
		case map[string]any:
			copy[i] = deepCopyMap(v)
		case []any:
			copy[i] = deepCopySlice(v)
		default:
			copy[i] = v
		}
	}
	return copy
}
