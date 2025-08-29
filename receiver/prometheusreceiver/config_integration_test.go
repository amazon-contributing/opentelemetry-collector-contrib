// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusreceiver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureGroupConstantIntegration(t *testing.T) {
	// Test that the constant can be used in a real config structure and gets replaced correctly
	config := map[string]any{
		"scrape_configs": []any{
			map[string]any{
				"job_name": "test-job",
				"relabel_configs": []any{
					map[string]any{
						"source_labels": []any{"__meta_kubernetes_pod_name"},
						"target_label":  "pod",
						"replacement":   Escaped_CaptureGroupOne,
					},
					map[string]any{
						"source_labels": []any{"__meta_kubernetes_service_name"},
						"target_label":  "service",
						"replacement":   "static_value",
					},
				},
			},
		},
	}

	// Before preprocessing, the constant should be present
	scrapeConfigs := config["scrape_configs"].([]any)
	relabelConfigs := scrapeConfigs[0].(map[string]any)["relabel_configs"].([]any)
	firstRelabelConfig := relabelConfigs[0].(map[string]any)
	assert.Equal(t, Escaped_CaptureGroupOne, firstRelabelConfig["replacement"])

	// Apply preprocessing
	preprocessPrometheusConfig(config)

	// After preprocessing, the constant should be replaced with "$1"
	scrapeConfigsAfter := config["scrape_configs"].([]any)
	relabelConfigsAfter := scrapeConfigsAfter[0].(map[string]any)["relabel_configs"].([]any)
	firstRelabelConfigAfter := relabelConfigsAfter[0].(map[string]any)
	secondRelabelConfigAfter := relabelConfigsAfter[1].(map[string]any)

	assert.Equal(t, "$1", firstRelabelConfigAfter["replacement"])
	assert.Equal(t, "static_value", secondRelabelConfigAfter["replacement"]) // Should remain unchanged
}

func TestUnmarshalYAMLIntegration(t *testing.T) {
	// Test that unmarshalYAML correctly applies preprocessing
	input := map[string]any{
		"global": map[string]any{
			"scrape_interval": "15s",
		},
		"scrape_configs": []any{
			map[string]any{
				"job_name": "kubernetes-pods",
				"relabel_configs": []any{
					map[string]any{
						"source_labels": []any{"__meta_kubernetes_pod_name"},
						"target_label":  "kubernetes_pod_name",
						"replacement":   Escaped_CaptureGroupOne,
					},
				},
			},
		},
	}

	var result map[string]any
	err := unmarshalYAML(input, &result)
	require.NoError(t, err)

	// Verify that the constant was replaced during unmarshaling
	scrapeConfigs := input["scrape_configs"].([]any)
	relabelConfigs := scrapeConfigs[0].(map[string]any)["relabel_configs"].([]any)
	firstRelabelConfig := relabelConfigs[0].(map[string]any)

	assert.Equal(t, "$1", firstRelabelConfig["replacement"])
}
