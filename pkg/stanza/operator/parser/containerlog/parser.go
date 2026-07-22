// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package containerlog // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/parser/containerlog"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

// errMaxLogLen is the maximum number of bytes of a raw log line to include in
// parse-error messages. Longer lines are truncated to keep error logs bounded
// while preserving enough context for debugging (about half a typical CRI line).
const errMaxLogLen = 128

// Parser is a stanza operator that parses CRI and Docker JSON container log
// formats into structured OTEL log entries.
//
// Concurrency: Parser is not safe for concurrent use from multiple goroutines.
// This matches the stanza operator contract — the filelog receiver processes
// entries sequentially through the operator chain on a single goroutine.
type Parser struct {
	helper.TransformerOperator
}

// ProcessBatch is required by the operator.Operator interface. There is no
// batch-specific optimization — each entry is processed individually via Process.
func (p *Parser) ProcessBatch(ctx context.Context, entries []*entry.Entry) error {
	return p.ProcessBatchWith(ctx, entries, p.Process)
}

// Process detects the container log format via a single byte check and parses
// accordingly. Honors the `if` expression from TransformerConfig — when
// configured and the expression doesn't match, the entry is passed through
// unchanged. Parse failures are routed through HandleEntryError to respect
// the on_error strategy.
func (p *Parser) Process(ctx context.Context, e *entry.Entry) error {
	// Honor the `if` expression — skip parsing when condition doesn't match
	skip, err := p.Skip(ctx, e)
	if err != nil {
		return p.HandleEntryError(ctx, e, err)
	}
	if skip {
		return p.Write(ctx, e)
	}

	raw, ok := e.Body.(string)
	if !ok || len(raw) == 0 {
		return p.Write(ctx, e)
	}

	var parseErr error
	if raw[0] == '{' {
		parseErr = p.parseDocker(e, raw)
	} else {
		parseErr = p.parseCRI(e, raw)
	}

	if parseErr != nil {
		return p.HandleEntryError(ctx, e, parseErr)
	}

	return p.Write(ctx, e)
}

// parseCRI handles CRI format: "<timestamp> <stream> <flags> <log>"
//
// Extracted fields are copied out of raw so the original input can be
// garbage-collected as soon as parseCRI returns. The copy cost is a few
// hundred nanoseconds per line; in exchange the operator has no hidden memory
// pinning regardless of how long downstream buffers the entry.
func (p *Parser) parseCRI(e *entry.Entry, raw string) error {
	// Find the three space separators delimiting timestamp / stream / flags / body.
	// strings.IndexByte(raw[start:], ' ') returns an offset within the slice,
	// not within raw — translate it back by adding the slice start.
	i1 := strings.IndexByte(raw, ' ')
	if i1 < 0 {
		return fmt.Errorf("CRI parse: expected 3 space-separated fields in %q", truncate(raw, errMaxLogLen))
	}
	i2 := strings.IndexByte(raw[i1+1:], ' ')
	if i2 < 0 {
		return fmt.Errorf("CRI parse: expected 3 space-separated fields in %q", truncate(raw, errMaxLogLen))
	}
	i2 += i1 + 1 // translate to absolute index in raw
	i3 := strings.IndexByte(raw[i2+1:], ' ')
	if i3 < 0 {
		return fmt.Errorf("CRI parse: expected 3 space-separated fields in %q", truncate(raw, errMaxLogLen))
	}
	i3 += i2 + 1 // translate to absolute index in raw

	// Extract substrings — still slices at this point. They must be copied
	// before being stored on the entry or they will pin raw in memory.
	timeStr := raw[:i1]
	stream := raw[i1+1 : i2]
	flags := raw[i2+1 : i3]
	body := raw[i3+1:]

	// Parse timestamp first; parseTimestamp does its own allocation (time.Time
	// is a value type). timeStr does not need to be copied.
	parseTimestamp(e, timeStr)

	// Set attributes (OTEL semantic conventions). Intern the common stream
	// and flag values so they don't alias into raw. Unusual values fall back
	// to strings.Clone.
	if e.Attributes == nil {
		e.Attributes = make(map[string]any, 2)
	}
	e.Attributes["log.iostream"] = internStream(stream)
	// logtag accepts any value between the 2nd and 3rd space — not validated
	// against known CRI flags (F/P). This is intentional for forward-compatibility
	// with future container runtime flag values.
	//
	// Coupling note: the downstream recombine operator uses
	// is_last_entry: "attributes.logtag == 'F'" as its terminator. An unexpected
	// flag value (runtime bug, corruption) will cause recombine to buffer lines
	// indefinitely until its max_log_size safety net triggers. This is acceptable
	// because recombine is opt-in and has its own bounded-memory protection.
	e.Attributes["logtag"] = internLogtag(flags)

	// Body is the largest field and the main memory-pinning risk — always clone.
	e.Body = strings.Clone(body)
	return nil
}

// internStream returns a constant string for the common CRI stream values,
// avoiding any aliasing into the input. Unusual values are cloned.
func internStream(s string) string {
	switch s {
	case "stdout":
		return "stdout"
	case "stderr":
		return "stderr"
	default:
		return strings.Clone(s)
	}
}

// internLogtag returns a constant string for the common CRI flag values,
// avoiding any aliasing into the input. Unusual values are cloned.
func internLogtag(s string) string {
	switch s {
	case "F":
		return "F"
	case "P":
		return "P"
	default:
		return strings.Clone(s)
	}
}

// dockerLog matches the Docker JSON log format fields.
type dockerLog struct {
	Log    string `json:"log"`
	Stream string `json:"stream"`
	Time   string `json:"time"`
}

// parseDocker handles Docker JSON format: {"log":"...","stream":"...","time":"..."}
// Requires the `time` field to be non-empty to distinguish Docker runtime JSON
// from application-emitted JSON logs (which would otherwise be destructively parsed).
// A non-empty but malformed `time` value is accepted — the body is parsed but
// Timestamp is left unset (parseTimestamp fails silently). This is acceptable
// because kubelet does not emit garbage timestamps.
func (p *Parser) parseDocker(e *entry.Entry, raw string) error {
	var dl dockerLog
	if err := json.Unmarshal([]byte(raw), &dl); err != nil {
		return fmt.Errorf("Docker JSON parse: %w", err)
	}

	// Sanity gate: Docker runtime always sets the `time` field. If it's empty,
	// this is likely application-emitted JSON (e.g., {"level":"info","msg":"..."})
	// and we must not destroy the original body.
	if dl.Time == "" {
		return errors.New("Docker JSON parse: missing required 'time' field, likely not Docker runtime format")
	}

	if e.Attributes == nil {
		e.Attributes = make(map[string]any, 1)
	}
	// Use string constants for the common values so every Docker entry
	// shares one string instead of allocating a fresh copy per parse.
	switch dl.Stream {
	case "":
		// no iostream attribute
	case "stdout":
		e.Attributes["log.iostream"] = "stdout"
	case "stderr":
		e.Attributes["log.iostream"] = "stderr"
	default:
		e.Attributes["log.iostream"] = dl.Stream
	}

	e.Body = strings.TrimSuffix(dl.Log, "\n")
	parseTimestamp(e, dl.Time)
	return nil
}

// parseTimestamp sets e.Timestamp from an RFC3339Nano time string.
// On parse failure, e.Timestamp is left unchanged.
func parseTimestamp(e *entry.Entry, s string) {
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		e.Timestamp = ts
	}
}

// truncate returns s shortened to at most maxLen bytes for use in error messages.
// The cut is backed up to the nearest rune boundary to avoid splitting a
// multi-byte UTF-8 sequence, which would otherwise produce escape-sequence
// noise in logs.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Back up to a rune start so we don't cut mid-rune.
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	return s[:maxLen] + "..."
}
