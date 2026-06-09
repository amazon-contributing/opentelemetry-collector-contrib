// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package containerlog

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/testutil"
)

func buildTestParser(t *testing.T) *Parser {
	t.Helper()
	cfg := NewConfigWithID("test")
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)
	return op.(*Parser)
}

// processAndReceive processes body through a default-config parser and returns
// the received entry. If attrs is supplied, it is set on the entry before
// processing (used to verify that existing attributes are preserved).
func processAndReceive(t *testing.T, body any, attrs ...map[string]any) *entry.Entry {
	t.Helper()
	p := buildTestParser(t)
	fake := testutil.NewFakeOutput(t)
	p.SetOutputIDs([]string{fake.ID()})
	require.NoError(t, p.SetOutputs([]operator.Operator{fake}))

	e := entry.New()
	e.Body = body
	if len(attrs) > 0 {
		e.Attributes = attrs[0]
	}
	_ = p.Process(t.Context(), e) // may return error on malformed input (on_error: send still sends)

	select {
	case received := <-fake.Received:
		return received
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for entry")
		return nil
	}
}

// --- CRI format tests ---

func TestCRI_HappyPath_StdoutFull(t *testing.T) {
	e := processAndReceive(t, "2026-05-05T14:05:18.123456789Z stdout F hello world")

	require.Equal(t, "hello world", e.Body)
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
	require.Equal(t, "F", e.Attributes["logtag"])
	require.Equal(t, time.Date(2026, 5, 5, 14, 5, 18, 123456789, time.UTC), e.Timestamp)
}

func TestCRI_HappyPath_StderrPartial(t *testing.T) {
	e := processAndReceive(t, "2026-05-05T14:05:18Z stderr P partial line")

	require.Equal(t, "partial line", e.Body)
	require.Equal(t, "stderr", e.Attributes["log.iostream"])
	require.Equal(t, "P", e.Attributes["logtag"])
}

func TestCRI_EmptyBodyAfterFlags(t *testing.T) {
	e := processAndReceive(t, "2026-05-05T14:05:18Z stdout F ")

	require.Empty(t, e.Body)
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
	require.Equal(t, "F", e.Attributes["logtag"])
}

func TestCRI_LongBody(t *testing.T) {
	longBody := strings.Repeat("x", 10240)
	e := processAndReceive(t, "2026-05-05T14:05:18Z stdout F "+longBody)

	require.Equal(t, longBody, e.Body)
}

func TestCRI_UnicodeBody(t *testing.T) {
	e := processAndReceive(t, "2026-05-05T14:05:18Z stdout F 日本語テスト 🎉")

	require.Equal(t, "日本語テスト 🎉", e.Body)
}

func TestCRI_CriOTimestampWithOffset(t *testing.T) {
	// cri-o uses RFC3339Nano with timezone offset instead of Z
	e := processAndReceive(t, "2026-05-05T14:05:18.123456789+05:30 stdout F crio line")

	require.Equal(t, "crio line", e.Body)
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
	require.Equal(t, "F", e.Attributes["logtag"])
	expected := time.Date(2026, 5, 5, 14, 5, 18, 123456789, time.FixedZone("", 5*3600+30*60))
	require.Equal(t, expected, e.Timestamp)
}

func TestCRI_ContainerdTimestamp(t *testing.T) {
	// containerd uses shorter fractional seconds with Z
	e := processAndReceive(t, "2026-05-05T14:05:18.123Z stderr F containerd line")

	require.Equal(t, "containerd line", e.Body)
	require.Equal(t, "stderr", e.Attributes["log.iostream"])
	expected := time.Date(2026, 5, 5, 14, 5, 18, 123000000, time.UTC)
	require.Equal(t, expected, e.Timestamp)
}

func TestCRI_Malformed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "NoSpaces", body: "nospaces"},
		{name: "OneSpace", body: "timestamp stream"},
		{name: "TwoSpaces", body: "timestamp stream flags"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := processAndReceive(t, tc.body)
			require.Equal(t, tc.body, e.Body)
			require.NotContains(t, e.Attributes, "log.iostream")
			require.NotContains(t, e.Attributes, "logtag")
		})
	}
}

func TestCRI_UnusualStreamValue(t *testing.T) {
	// Exercises the default (clone) branch of internStream. Non-standard stream
	// names are accepted verbatim for forward-compat with future runtimes.
	e := processAndReceive(t, "2026-05-05T14:05:18Z customstream F hello")

	require.Equal(t, "hello", e.Body)
	require.Equal(t, "customstream", e.Attributes["log.iostream"])
	require.Equal(t, "F", e.Attributes["logtag"])
}

func TestCRI_UnusualLogtagValue(t *testing.T) {
	// Exercises the default (clone) branch of internLogtag. Non-standard flag
	// values are accepted verbatim for forward-compat.
	e := processAndReceive(t, "2026-05-05T14:05:18Z stdout X hello")

	require.Equal(t, "hello", e.Body)
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
	require.Equal(t, "X", e.Attributes["logtag"])
}

// --- Docker JSON format tests ---

func TestDocker_HappyPath(t *testing.T) {
	e := processAndReceive(t, `{"log":"hello world\n","stream":"stdout","time":"2026-05-05T14:05:18.123Z"}`)

	require.Equal(t, "hello world", e.Body)
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
	require.Equal(t, time.Date(2026, 5, 5, 14, 5, 18, 123000000, time.UTC), e.Timestamp)
}

func TestDocker_ExtraFieldsIgnored(t *testing.T) {
	e := processAndReceive(t, `{"log":"hi\n","stream":"stderr","time":"2026-05-05T14:05:18Z","extra":"ignored","nested":{"a":1}}`)

	require.Equal(t, "hi", e.Body)
	require.Equal(t, "stderr", e.Attributes["log.iostream"])
}

func TestDocker_MalformedJSON(t *testing.T) {
	e := processAndReceive(t, `{not valid json`)
	require.Equal(t, `{not valid json`, e.Body)
}

func TestDocker_EmptyLog(t *testing.T) {
	// Defensive edge case: kubelet wouldn't emit an empty log with a timestamp,
	// but we verify the parser handles it without panicking.
	e := processAndReceive(t, `{"log":"","stream":"stdout","time":"2026-05-05T14:05:18Z"}`)

	require.Empty(t, e.Body)
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
}

func TestDocker_MissingTimeField(t *testing.T) {
	// Missing `time` field triggers the sanity gate. parseDocker returns the error
	// before touching e.Body, so the original body is preserved regardless of
	// on_error mode. With default on_error: send, HandleEntryError sends it downstream.
	e := processAndReceive(t, `{"log":"hello\n","stream":"stdout"}`)

	// Body is unchanged because the sanity gate rejected this as non-Docker-runtime JSON
	require.Equal(t, `{"log":"hello\n","stream":"stdout"}`, e.Body)
}

func TestDocker_ApplicationJSON_NotCorrupted(t *testing.T) {
	// Application-emitted structured JSON (no `time` field) must not be destroyed.
	// This is the data-loss scenario the sanity gate prevents.
	input := `{"level":"info","msg":"request handled","latency_ms":42}`
	e := processAndReceive(t, input)

	require.Equal(t, input, e.Body) // body preserved unchanged
}

func TestDocker_EmptyStreamField(t *testing.T) {
	// `stream` field present but empty — no log.iostream attribute is set.
	e := processAndReceive(t, `{"log":"hello\n","stream":"","time":"2026-05-05T14:05:18Z"}`)

	require.Equal(t, "hello", e.Body)
	require.NotContains(t, e.Attributes, "log.iostream")
}

func TestDocker_UnusualStreamValue(t *testing.T) {
	// Non-standard stream value falls into the default branch — stored verbatim.
	e := processAndReceive(t, `{"log":"hello\n","stream":"debug","time":"2026-05-05T14:05:18Z"}`)

	require.Equal(t, "hello", e.Body)
	require.Equal(t, "debug", e.Attributes["log.iostream"])
}

// --- Edge cases ---

func TestProcess_NonStringBody(t *testing.T) {
	e := processAndReceive(t, 42)
	require.Equal(t, 42, e.Body)
}

func TestProcess_NilBody(t *testing.T) {
	e := processAndReceive(t, nil)
	require.Nil(t, e.Body)
}

func TestProcess_EmptyStringBody(t *testing.T) {
	e := processAndReceive(t, "")
	require.Empty(t, e.Body)
}

func TestProcess_EmptyStringBody_PreservesAttributes(t *testing.T) {
	attrs := map[string]any{
		"log.file.path": "/var/log/containers/test.log",
		"existing":      "value",
	}
	e := processAndReceive(t, "", attrs)

	require.Empty(t, e.Body)
	require.Equal(t, "/var/log/containers/test.log", e.Attributes["log.file.path"])
	require.Equal(t, "value", e.Attributes["existing"])
}

func TestProcess_ExistingAttributesPreserved(t *testing.T) {
	attrs := map[string]any{
		"log.file.path": "/var/log/containers/test.log",
		"existing":      "value",
	}
	e := processAndReceive(t, "2026-05-05T14:05:18Z stdout F hello", attrs)

	require.Equal(t, "hello", e.Body)
	require.Equal(t, "/var/log/containers/test.log", e.Attributes["log.file.path"])
	require.Equal(t, "value", e.Attributes["existing"])
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
	require.Equal(t, "F", e.Attributes["logtag"])
}

func TestDocker_ExistingAttributesPreserved(t *testing.T) {
	attrs := map[string]any{
		"log.file.path": "/var/log/containers/test.log",
		"existing":      "value",
	}
	e := processAndReceive(t,
		`{"log":"hello\n","stream":"stdout","time":"2026-05-05T14:05:18Z"}`, attrs)

	require.Equal(t, "hello", e.Body)
	require.Equal(t, "/var/log/containers/test.log", e.Attributes["log.file.path"])
	require.Equal(t, "value", e.Attributes["existing"])
	require.Equal(t, "stdout", e.Attributes["log.iostream"])
}

func TestDocker_ExistingAttributesPreserved_OnSanityGateFail(t *testing.T) {
	// Missing `time` field — sanity gate rejects. Existing attributes and body
	// must be untouched.
	input := `{"level":"info","msg":"request handled"}`
	attrs := map[string]any{"log.file.path": "/var/log/containers/test.log"}
	e := processAndReceive(t, input, attrs)

	require.Equal(t, input, e.Body)
	require.Equal(t, "/var/log/containers/test.log", e.Attributes["log.file.path"])
	require.NotContains(t, e.Attributes, "log.iostream")
}

func TestDocker_ExistingAttributesPreserved_OnMalformedJSON(t *testing.T) {
	// json.Unmarshal fails — entry must be untouched.
	input := `{not valid json`
	attrs := map[string]any{"log.file.path": "/var/log/containers/test.log"}
	e := processAndReceive(t, input, attrs)

	require.Equal(t, input, e.Body)
	require.Equal(t, "/var/log/containers/test.log", e.Attributes["log.file.path"])
	require.NotContains(t, e.Attributes, "log.iostream")
}

// --- Framework contract tests ---

// buildConfiguredParser constructs a Parser with a caller-supplied config closure
// so tests can exercise non-default TransformerConfig settings (IfExpr, OnError).
func buildConfiguredParser(t *testing.T, configure func(*Config)) *Parser {
	t.Helper()
	cfg := NewConfigWithID("test")
	if configure != nil {
		configure(cfg)
	}
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)
	return op.(*Parser)
}

func TestProcessBatch(t *testing.T) {
	p := buildTestParser(t)
	fake := testutil.NewFakeOutput(t)
	p.SetOutputIDs([]string{fake.ID()})
	require.NoError(t, p.SetOutputs([]operator.Operator{fake}))

	criHappy := entry.New()
	criHappy.Body = "2026-05-05T14:05:18Z stdout F first"

	dockerHappy := entry.New()
	dockerHappy.Body = `{"log":"second\n","stream":"stdout","time":"2026-05-05T14:05:18Z"}`

	malformed := entry.New()
	malformed.Body = "nospaces-and-no-runtime-format"
	malformedOriginalTS := malformed.Timestamp

	// Default on_error=send aggregates the malformed error via multierr
	// but writes every entry through the pipeline.
	err := p.ProcessBatch(t.Context(), []*entry.Entry{criHappy, dockerHappy, malformed})
	require.Error(t, err, "malformed entry surfaces error under default on_error=send")

	// ProcessBatchWith is sequential, so received order matches input order.
	received := make([]*entry.Entry, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case e := <-fake.Received:
			received = append(received, e)
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d entries", len(received))
		}
	}

	// Entry 0: CRI happy path — parsed body, attrs, and timestamp.
	expectedTS := time.Date(2026, 5, 5, 14, 5, 18, 0, time.UTC)
	require.Equal(t, "first", received[0].Body)
	require.Equal(t, "stdout", received[0].Attributes["log.iostream"])
	require.Equal(t, "F", received[0].Attributes["logtag"])
	require.Equal(t, expectedTS, received[0].Timestamp)

	// Entry 1: Docker happy path — parsed body, attrs, and timestamp.
	require.Equal(t, "second", received[1].Body)
	require.Equal(t, "stdout", received[1].Attributes["log.iostream"])
	require.Equal(t, expectedTS, received[1].Timestamp)

	// Entry 2: malformed — body/attrs/timestamp must all be untouched.
	require.Equal(t, "nospaces-and-no-runtime-format", received[2].Body)
	require.NotContains(t, received[2].Attributes, "log.iostream")
	require.Equal(t, malformedOriginalTS, received[2].Timestamp)
}

func TestProcess_IfExpression(t *testing.T) {
	const criLine = "2026-05-05T14:05:18Z stdout F hello"

	cases := []struct {
		name       string
		ifExpr     string
		wantBody   any
		wantStream any // nil means attribute must not be set
		wantErr    bool
	}{
		{name: "NoIf", ifExpr: "", wantBody: "hello", wantStream: "stdout"},
		{name: "TrueLiteral", ifExpr: "true", wantBody: "hello", wantStream: "stdout"},
		{name: "FalseLiteral_SkipsParse", ifExpr: "false", wantBody: criLine, wantStream: nil},
		{name: "EvaluatedTrue", ifExpr: `body startsWith "2026"`, wantBody: "hello", wantStream: "stdout"},
		{name: "EvaluatedFalse_SkipsParse", ifExpr: `body startsWith "nomatch"`, wantBody: criLine, wantStream: nil},
		{name: "FailingExpr", ifExpr: `body.nonexistent == "x"`, wantBody: criLine, wantStream: nil, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := buildConfiguredParser(t, func(c *Config) { c.IfExpr = tc.ifExpr })
			fake := testutil.NewFakeOutput(t)
			p.SetOutputIDs([]string{fake.ID()})
			require.NoError(t, p.SetOutputs([]operator.Operator{fake}))

			e := entry.New()
			e.Body = criLine
			err := p.Process(t.Context(), e)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			select {
			case received := <-fake.Received:
				require.Equal(t, tc.wantBody, received.Body)
				if tc.wantStream == nil {
					require.NotContains(t, received.Attributes, "log.iostream")
				} else {
					require.Equal(t, tc.wantStream, received.Attributes["log.iostream"])
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for entry")
			}
		})
	}
}

func TestProcess_OnErrorModes(t *testing.T) {
	const malformed = "nospaces-garbage"

	// Every mode returns the parse error from HandleEntryError; they differ
	// only in whether the entry is then forwarded (send*) or dropped (drop*).
	cases := []struct {
		name     string
		onError  string
		wantSent bool
	}{
		{name: "DefaultSend", onError: "", wantSent: true},
		{name: "Send", onError: "send", wantSent: true},
		{name: "SendQuiet", onError: "send_quiet", wantSent: true},
		{name: "Drop", onError: "drop", wantSent: false},
		{name: "DropQuiet", onError: "drop_quiet", wantSent: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := buildConfiguredParser(t, func(c *Config) {
				if tc.onError != "" {
					c.OnError = tc.onError
				}
			})
			fake := testutil.NewFakeOutput(t)
			p.SetOutputIDs([]string{fake.ID()})
			require.NoError(t, p.SetOutputs([]operator.Operator{fake}))

			e := entry.New()
			e.Body = malformed
			err := p.Process(t.Context(), e)
			require.Error(t, err)

			if tc.wantSent {
				fake.ExpectBody(t, malformed)
			} else {
				fake.ExpectNoEntry(t, 100*time.Millisecond)
			}
		})
	}
}

// --- Internal helper tests ---

func TestParseTimestamp_NonUTC(t *testing.T) {
	// Exercises the time-offset branch of time.Parse (not covered by CRI/Docker
	// happy paths which always use UTC "Z").
	e := entry.New()
	parseTimestamp(e, "2026-05-05T14:05:18.123+05:30")

	expected := time.Date(2026, 5, 5, 14, 5, 18, 123000000, time.FixedZone("", 5*3600+30*60))
	require.Equal(t, expected, e.Timestamp)
}

func TestParseTimestamp_Malformed_Unchanged(t *testing.T) {
	// Pins the contract: on parse failure, e.Timestamp is left alone (not zeroed).
	e := entry.New()
	originalTimestamp := e.Timestamp
	parseTimestamp(e, "not-a-timestamp")
	require.Equal(t, originalTimestamp, e.Timestamp)
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{name: "Empty", in: "", maxLen: 10, want: ""},
		{name: "Shorter", in: "hello", maxLen: 10, want: "hello"},
		{name: "ExactLength", in: "hello", maxLen: 5, want: "hello"},
		{name: "Longer_ASCII", in: "hello world", maxLen: 5, want: "hello..."},
		{name: "ZeroMaxLen", in: "hello", maxLen: 0, want: "..."},
		{name: "CutMidRune_BacksUp", in: "héllo", maxLen: 2, want: "h..."},             // é is 2 bytes starting at index 1
		{name: "CutInside3ByteRune_BacksUp", in: "日本語", maxLen: 5, want: "日..."},       // 本 occupies bytes 3-5
		{name: "CutAtRuneBoundary_Keeps", in: "日本語", maxLen: 6, want: "日本..."},         // byte 6 starts 語
		{name: "InvalidUTF8_DoesNotPanic", in: "\x80\x80\x80", maxLen: 1, want: "..."}, // all continuation bytes — back-up walks to 0
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, truncate(tc.in, tc.maxLen))
		})
	}
}

// --- Benchmarks ---

// BenchmarkContainerLogParser_CRI benchmarks the parseCRI hot path in isolation
// on a realistic 280-byte CRI log line. Includes entry.New() allocation to
// measure the full per-log-line cost customers see (entry creation + parse).
func BenchmarkContainerLogParser_CRI(b *testing.B) {
	cfg := NewConfigWithID("bench")
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	if err != nil {
		b.Fatal(err)
	}
	p := op.(*Parser)

	// Realistic CRI line: ~280 bytes
	padding := strings.Repeat("x", 240)
	line := "2026-05-05T14:05:18.123456789Z stdout F " + padding

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := entry.New()
		e.Body = line
		_ = p.parseCRI(e, line)
	}
}

// BenchmarkContainerLogParser_Docker benchmarks Docker JSON parsing end-to-end.
// Includes entry.New() + json.Unmarshal allocations — measures full per-entry cost.
func BenchmarkContainerLogParser_Docker(b *testing.B) {
	cfg := NewConfigWithID("bench")
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	if err != nil {
		b.Fatal(err)
	}
	p := op.(*Parser)

	line := `{"log":"` + strings.Repeat("x", 240) + `\n","stream":"stdout","time":"2026-05-05T14:05:18.123Z"}`

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := entry.New()
		e.Body = line
		_ = p.parseDocker(e, line)
	}
}

// BenchmarkProcess_CRI benchmarks the full Process path (type check, format
// detection, parse, Write) with a connected NopOutput. This is the number to
// compare against the upstream container operator's full Process path.
func BenchmarkProcess_CRI(b *testing.B) {
	cfg := NewConfigWithID("bench")
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	if err != nil {
		b.Fatal(err)
	}
	p := op.(*Parser)

	fake := testutil.NewFakeOutput(b)
	p.SetOutputIDs([]string{fake.ID()})
	if err := p.SetOutputs([]operator.Operator{fake}); err != nil {
		b.Fatal(err)
	}

	padding := strings.Repeat("x", 240)
	line := "2026-05-05T14:05:18.123456789Z stdout F " + padding
	ctx := b.Context()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := entry.New()
		e.Body = line
		_ = p.Process(ctx, e)
		// Drain the fake output channel to prevent blocking
		<-fake.Received
	}
}

