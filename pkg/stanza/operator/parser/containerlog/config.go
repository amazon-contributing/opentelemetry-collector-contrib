// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package containerlog provides a high-performance container log parser
// that handles both CRI and Docker JSON formats. Format detection uses a single
// byte check ('{' = Docker JSON, anything else = CRI) instead of regex,
// eliminating the need for a router operator.
//
// This operator is a performance-optimized alternative to the upstream
// "container" operator. It achieves a large CPU reduction at high log volumes
// through zero-allocation string-indexing for CRI parsing. Timestamp parsing
// uses the standard library (time.Parse) — the marginal cost is under 1% of
// total pipeline CPU and not worth a custom implementation.
//
// # Supported Formats
//
//   - CRI (containerd/cri-o): <timestamp> <stream> <flags> <log_body>
//   - Docker JSON: {"log":"...","stream":"...","time":"..."}
//
// # Limitations
//
// This operator uses helper.TransformerConfig rather than helper.ParserConfig,
// so parse_from, parse_to, and preserve_to are not supported. In practice these
// knobs are rarely used for container log parsing — filelog places the line in
// body, and the unwrapped log belongs in body. If you need to retain the raw
// wrapped line for auditing or debugging, use a copy operator upstream:
//
//   - type: copy
//     from: body
//     to: attributes["raw_line"]
//   - type: container_log_parser
//
// For pipelines that genuinely need parse_from / parse_to (rare), use the
// upstream "container" operator instead.
//
// This operator does not perform multi-line recombination. To join partial
// log lines (CRI P flag), add the recombine operator downstream. The logtag
// attribute set by this operator is compatible with recombine's default
// is_last_entry expression: attributes.logtag == 'F'.
//
// # Error Handling
//
// Parse failures (malformed CRI, invalid Docker JSON) are routed through
// HandleEntryError which respects the on_error config. The default (send)
// logs each failure at ERROR level with all entry attributes. For production
// pipelines where non-container files might accidentally be tailed, configure
// on_error: send_quiet or drop_quiet to suppress per-entry error logging.
package containerlog // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/containerlog"

import (
	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

const operatorType = "container_log_parser"

func init() {
	operator.Register(operatorType, func() operator.Builder { return NewConfig() })
}

// Config is the configuration for the container_log_parser operator.
type Config struct {
	helper.TransformerConfig `mapstructure:",squash"`
}

// NewConfig creates a new container_log_parser config with default values.
func NewConfig() *Config {
	return NewConfigWithID(operatorType)
}

// NewConfigWithID creates a new container_log_parser config with the given ID.
func NewConfigWithID(operatorID string) *Config {
	return &Config{
		TransformerConfig: helper.NewTransformerConfig(operatorID, operatorType),
	}
}

// Build creates the container_log_parser operator from config.
func (c Config) Build(set component.TelemetrySettings) (operator.Operator, error) {
	transformer, err := c.TransformerConfig.Build(set)
	if err != nil {
		return nil, err
	}
	return &Parser{TransformerOperator: transformer}, nil
}
