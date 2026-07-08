// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package slurm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGRESGPUIndices(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []int
	}{
		{
			name:     "single GPU",
			input:    "gpu:tesla_t4:1(IDX:0)",
			expected: []int{0},
		},
		{
			name:     "multiple GPUs range",
			input:    "gpu:a100:4(IDX:0-3)",
			expected: []int{0, 1, 2, 3},
		},
		{
			name:     "multiple GPUs comma-separated",
			input:    "gpu:a100:2(IDX:0,2)",
			expected: []int{0, 2},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "no IDX pattern",
			input:    "gpu:tesla_t4:1",
			expected: nil,
		},
		{
			name:     "mixed range and single",
			input:    "gpu:h100:5(IDX:0-2,4,6)",
			expected: []int{0, 1, 2, 4, 6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseGRESGPUIndices(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDedupStrings(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, dedupStrings([]string{"a", "b", "a", "c", "b"}))
	assert.Equal(t, []string{"x"}, dedupStrings([]string{"x", "x", "x"}))
	assert.Empty(t, dedupStrings(nil))
}
