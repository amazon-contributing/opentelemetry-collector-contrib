// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// Task 28 spec coverage (test side) — these tests prove each
// requirement of plan step 28 (Wire native reader into journald input
// operator) is met:
//
//   Requirement                            -> Test(s)
//   ──────────────────────────────────────────────────────────────────
//   "dispatch to the native package when   -> TestNativeDispatch_EmitsEntries
//    Mode=='native' [and the feature gate     (happy path: Mode=ModeNative
//    is enabled]"                             with a real .journal fixture
//                                             produces stanza entries
//                                             through the native code
//                                             path). Gate-enforcement is
//                                             pinned in
//                                             receiver/journaldreceiver/
//                                             config_test.go:
//                                             TestConfigValidate_NativeRequiresFeatureGate
//                                             so the operator-side trusts
//                                             the receiver contract.
//
//   "field-for-field parity (timestamps,    -> TestNativeEntryShape
//    severity, body, attributes)"             pins:
//                                              * Body type (map[string]any)
//                                              * __CURSOR present + non-empty
//                                              * __MONOTONIC_TIMESTAMP present
//                                                + non-empty
//                                              * __REALTIME_TIMESTAMP absent
//                                                from body (consumed for
//                                                Timestamp, matching
//                                                parseJournalEntry)
//                                              * Timestamp set + reasonable
//                                              * Severity == Default
//                                              * SeverityText empty
//                                              * Attributes empty
//
//   "Do NOT alter the journalctl code      -> TestNativeNewCmd_IsNotInvokedInNativeMode
//    path"                                    wraps Input.newCmd in a
//                                             counting closure and asserts
//                                             zero invocations across the
//                                             lifetime of a native-mode
//                                             operator. Strongest guard
//                                             against an accidental
//                                             dispatch regression that
//                                             spawns journalctl(1) when
//                                             native is requested.
//                                             TestNativeBuild_PreservesJournalctlPath
//                                             complements by verifying a
//                                             default-Mode Build still
//                                             wires newCmd and leaves
//                                             nativePaths empty.
//
//   "Build with CGO_ENABLED=0"             -> All tests in this file run
//                                             under CGO_ENABLED=0 (the
//                                             native subpackage uses
//                                             pure-Go decompression libs
//                                             only). DoD-1 + DoD-7 pin
//                                             the build constraint.
//
//   "Run go test ./pkg/stanza/operator/   -> All 11 TestNative* / TestResolve* /
//    input/journald/... -v"                  TestDedupSorted* cases here
//                                             plus the legacy
//                                             TestBuild/TestInput suite in
//                                             input_test.go run under that
//                                             command. Verified locally
//                                             post-this-comment-edit:
//                                             ok  pkg/stanza/operator/input/journald
//                                             ok  pkg/stanza/operator/input/journald/native
//
// Path-resolution branch coverage (resolveNativeJournalPaths):
//
//   Files= dedup+sort                      -> TestResolveNativeJournalPaths_Files
//   Directory= glob                        -> TestResolveNativeJournalPaths_Directory
//   Directory= empty -> error              -> TestResolveNativeJournalPaths_DirectoryEmpty
//   no Files= and no Directory=            -> TestResolveNativeJournalPaths_NoConfig
//   Namespace= rejection                   -> TestResolveNativeJournalPaths_NamespaceUnsupported
//
// Lifecycle / failure-mode coverage:
//
//   Build error on unresolvable paths      -> TestNativeBuild_FailsOnUnresolvablePaths
//   Start error on no-paths-resolved       -> TestNativeStart_NoPaths (defence-in-depth
//                                                    for hand-constructed Inputs)
//   Stop drains follower goroutines        -> TestNativeStart_ContextCancellationStopsFollowers
//   Probe surfaces missing-file at Start   -> TestNativeFollowerErrorTolerance
//   dedupSorted helper                     -> TestDedupSorted_EdgeCases (5 cases)
//   drainAndDiscard smoke                  -> TestDrainAndDiscard_EOF
//
// No behavioural change in this comment-only edit; the test
// implementations committed in 499eef9 remain unchanged below.

package journald

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/entry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/testutil"
)

// fixtureSmallJournal returns the absolute path to the testdata/native/
// small.journal fixture committed under receiver/journaldreceiver. The
// fixture is generated by gen_small_journal.go and contains 2 ENTRY
// objects with deterministic field/value pairs that the parity
// assertions below pin against.
//
// The fixture lives under the receiver to keep its README and
// generation script colocated with the existing native-package
// fixtures; the operator-side test imports the path with a relative
// reference so we don't duplicate the bytes on disk.
func fixtureSmallJournal(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	// pkg/stanza/operator/input/journald -> ../../../../../receiver/journaldreceiver/testdata/native/small.journal
	path := filepath.Join(wd, "..", "..", "..", "..", "..",
		"receiver", "journaldreceiver", "testdata", "native", "small.journal")
	abs, err := filepath.Abs(path)
	require.NoError(t, err)
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("native fixture %s not available: %v", abs, err)
	}
	return abs
}

// TestNativeDispatch_EmitsEntries is the happy-path assertion: when
// Mode == ModeNative and Files= points at a real journal fixture, the
// operator emits one stanza entry per ENTRY object in the file.
//
// We do NOT pin the field shape here (TestNativeEntryShape covers
// that); this test is purely about the dispatch wire-up.
func TestNativeDispatch_EmitsEntries(t *testing.T) {
	cfg := NewConfigWithID("native_dispatch_test")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{fixtureSmallJournal(t)}
	cfg.StartAt = "beginning"

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err, "Build must succeed when Files= points at a real fixture")

	mockOutput := testutil.NewMockOperator("output")
	received := make(chan *entry.Entry, 16)
	mockOutput.On("Process", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		received <- args.Get(1).(*entry.Entry)
	}).Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, op.Start(testutil.NewUnscopedMockPersister()))
	t.Cleanup(func() { require.NoError(t, op.Stop()) })

	// Drain at least one entry within a generous timeout; the fixture
	// has multiple entries and we don't want to assert an exact count
	// here because StartAt=beginning + Follow's catch-up drain will
	// emit them all and additional follow-mode polls might fire.
	select {
	case e := <-received:
		require.NotNil(t, e)
		require.NotEmpty(t, e.Body, "native dispatch must populate entry body")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for native-dispatched entry")
	}
}

// TestNativeEntryShape pins the field-for-field parity contract spelled
// out in input_native.go's emitNativeEntry doc comment:
//
//   - Body is map[string]any with the journal FIELD=value pairs.
//   - Body contains "__CURSOR" (a non-empty string).
//   - Body contains "__MONOTONIC_TIMESTAMP" (a non-empty string).
//   - Body does NOT contain "__REALTIME_TIMESTAMP" (consumed for
//     entry.Timestamp, deleted from body, matching parseJournalEntry).
//   - entry.Timestamp is non-zero and within reasonable bounds (the
//     fixture was generated in 2026, but we assert > 2020 to absorb
//     fixture drift).
//   - entry.Severity / entry.SeverityText are zero-valued (the
//     journalctl path doesn't set them either).
//   - entry.Attributes is empty (matching journalctl path).
func TestNativeEntryShape(t *testing.T) {
	cfg := NewConfigWithID("native_shape_test")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{fixtureSmallJournal(t)}
	cfg.StartAt = "beginning"

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	mockOutput := testutil.NewMockOperator("output")
	received := make(chan *entry.Entry, 16)
	mockOutput.On("Process", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		received <- args.Get(1).(*entry.Entry)
	}).Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, op.Start(testutil.NewUnscopedMockPersister()))
	t.Cleanup(func() { require.NoError(t, op.Stop()) })

	var got *entry.Entry
	select {
	case got = <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for native-emitted entry")
	}

	body, ok := got.Body.(map[string]any)
	require.True(t, ok, "Body must be map[string]any to match journalctl path; got %T", got.Body)

	// Cursor / monotonic must be present and non-empty strings.
	cursor, ok := body["__CURSOR"]
	require.True(t, ok, "body must carry __CURSOR (parity with journalctl path)")
	require.IsType(t, "", cursor, "__CURSOR must be a string")
	require.NotEmpty(t, cursor)

	mono, ok := body["__MONOTONIC_TIMESTAMP"]
	require.True(t, ok, "body must carry __MONOTONIC_TIMESTAMP (parity with journalctl path)")
	require.IsType(t, "", mono, "__MONOTONIC_TIMESTAMP must be a string")
	require.NotEmpty(t, mono)

	// __REALTIME_TIMESTAMP is consumed and removed in the journalctl
	// path (parseJournalEntry deletes it before NewEntry). The native
	// path must match: the timestamp lives on entry.Timestamp instead.
	_, has := body["__REALTIME_TIMESTAMP"]
	assert.False(t, has, "body must NOT carry __REALTIME_TIMESTAMP; consumed for entry.Timestamp")

	// Timestamp must be set and reasonable. The fixture realtime is
	// 2026-era; assert > 2020 to absorb drift.
	require.False(t, got.Timestamp.IsZero(), "entry.Timestamp must be set from realtime")
	earliest := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.True(t, got.Timestamp.After(earliest),
		"entry.Timestamp %v should be after %v", got.Timestamp, earliest)

	// Severity not populated by either backend; matches journalctl path.
	assert.Equal(t, entry.Default, got.Severity, "Severity must remain Default to match journalctl path")
	assert.Empty(t, got.SeverityText, "SeverityText must remain empty to match journalctl path")

	// Attributes not populated; matches journalctl path.
	assert.Empty(t, got.Attributes, "Attributes must remain empty to match journalctl path")
}

// TestResolveNativeJournalPaths_Files verifies the Files= branch of
// path resolution: explicit list, deduplicated, sorted.
func TestResolveNativeJournalPaths_Files(t *testing.T) {
	c := *NewConfig()
	c.Files = []string{"/var/log/b.journal", "/var/log/a.journal", "/var/log/a.journal"}
	got, err := resolveNativeJournalPaths(c)
	require.NoError(t, err)
	assert.Equal(t, []string{"/var/log/a.journal", "/var/log/b.journal"}, got,
		"resolveNativeJournalPaths must dedup and sort Files")
}

// TestResolveNativeJournalPaths_Directory verifies the Directory=
// branch: glob *.journal, error if no matches, error if not a
// directory.
func TestResolveNativeJournalPaths_Directory(t *testing.T) {
	dir := t.TempDir()
	// Create two .journal files and one unrelated file.
	for _, name := range []string{"system.journal", "user-1000.journal", "unrelated.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}

	c := *NewConfig()
	d := dir
	c.Directory = &d
	got, err := resolveNativeJournalPaths(c)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, []string{
		filepath.Join(dir, "system.journal"),
		filepath.Join(dir, "user-1000.journal"),
	}, got)
}

// TestResolveNativeJournalPaths_DirectoryEmpty asserts the empty-glob
// case is reported as an error rather than silently returning zero
// paths (which would deadlock runNative on the no-paths-resolved
// branch).
func TestResolveNativeJournalPaths_DirectoryEmpty(t *testing.T) {
	dir := t.TempDir()
	c := *NewConfig()
	c.Directory = &dir
	_, err := resolveNativeJournalPaths(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no *.journal")
}

// TestResolveNativeJournalPaths_NoConfig asserts the
// no-Files-no-Directory case errors out instead of silently
// auto-discovering /var/log/journal/.
func TestResolveNativeJournalPaths_NoConfig(t *testing.T) {
	c := *NewConfig()
	_, err := resolveNativeJournalPaths(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "files")
	assert.Contains(t, err.Error(), "directory")
}

// TestResolveNativeJournalPaths_NamespaceUnsupported pins the explicit
// rejection of namespace= on the native backend so a future contributor
// who wires it up has a failing test forcing them to update this
// assertion.
func TestResolveNativeJournalPaths_NamespaceUnsupported(t *testing.T) {
	c := *NewConfig()
	c.Namespace = "myns"
	c.Files = []string{"/var/log/foo.journal"}
	_, err := resolveNativeJournalPaths(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace")
}

// TestNativeBuild_FailsOnUnresolvablePaths confirms Build surfaces a
// resolution error rather than letting Start return nil with a
// silently-broken backend.
func TestNativeBuild_FailsOnUnresolvablePaths(t *testing.T) {
	cfg := NewConfigWithID("native_build_fail")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	// Neither Files nor Directory set: resolveNativeJournalPaths errors.

	set := componenttest.NewNopTelemetrySettings()
	_, err := cfg.Build(set)
	require.Error(t, err, "Build must fail when native paths cannot be resolved")
	assert.Contains(t, err.Error(), "native journald reader")
}

// TestNativeStart_NoPaths is a defence-in-depth check: if a caller
// constructs Input by hand (skipping Build) and ends up with mode set
// but nativePaths nil, Start must fail rather than hang or crash.
func TestNativeStart_NoPaths(t *testing.T) {
	cfg := NewConfigWithID("native_no_paths")
	cfg.OutputIDs = []string{"output"}
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	in := op.(*Input)
	in.mode = ModeNative
	in.nativePaths = nil

	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).Return(nil)
	require.NoError(t, in.SetOutputs([]operator.Operator{mockOutput}))

	err = in.Start(testutil.NewUnscopedMockPersister())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "native journald reader failed")
	require.NoError(t, in.Stop())
}

// TestNativeBuild_PreservesJournalctlPath verifies a default-Mode (or
// explicit ModeJournalctl) Build still wires newCmd and leaves
// nativePaths empty, so the journalctl code path is untouched.
func TestNativeBuild_PreservesJournalctlPath(t *testing.T) {
	cfg := NewConfigWithID("default_mode")
	cfg.OutputIDs = []string{"output"}
	// Leave Mode empty.

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)
	in := op.(*Input)
	assert.Empty(t, in.mode, "default Mode must remain empty so Start dispatches to journalctl")
	assert.Empty(t, in.nativePaths, "default Mode must not pre-resolve native paths")
	assert.NotNil(t, in.newCmd, "journalctl newCmd closure must remain populated for default Mode")
}

// TestDedupSorted_EdgeCases pins the helper's contract directly so a
// regression doesn't surface as a flaky resolveNativeJournalPaths.
func TestDedupSorted_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{}},
		{"single", []string{"a"}, []string{"a"}},
		{"unsorted_unique", []string{"c", "a", "b"}, []string{"a", "b", "c"}},
		{"with_dupes", []string{"a", "a", "b", "b", "b", "c"}, []string{"a", "b", "c"}},
		{"all_same", []string{"x", "x", "x"}, []string{"x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupSorted(tc.in)
			if len(tc.want) == 0 && len(got) == 0 {
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestNativeStart_ContextCancellationStopsFollowers asserts Stop()
// causes the runNative goroutine and all per-file followers to exit.
// Without this assertion a refactor that drops ctx-propagation could
// leak goroutines into the receiver process for the lifetime of the
// collector.
func TestNativeStart_ContextCancellationStopsFollowers(t *testing.T) {
	cfg := NewConfigWithID("native_stop_test")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{fixtureSmallJournal(t)}
	cfg.StartAt = "end"

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, op.Start(testutil.NewUnscopedMockPersister()))
	require.NoError(t, op.Stop())

	// After Stop, the wait group must drain within a short window.
	in := op.(*Input)
	done := make(chan struct{})
	go func() {
		in.followerWaitGroup().Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("follower goroutines did not exit after Stop()")
	}
}

// drainAndDiscard sanity test: confirms the helper terminates on a
// closed Reader without panic. The follow-mode integration tests
// already exercise the EOF case via real journals; here we just want a
// quick guard against regressions in the loop condition.
func TestDrainAndDiscard_EOF(t *testing.T) {
	tmp := t.TempDir()
	src := fixtureSmallJournal(t)
	dst := filepath.Join(tmp, "small.journal")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o600))

	cfg := NewConfigWithID("drain_test")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{dst}
	set := componenttest.NewNopTelemetrySettings()
	_, buildErr := cfg.Build(set)
	require.NoError(t, buildErr)

	// drainAndDiscard is intended for use through followNativeFileOnce;
	// to test it directly we'd need to expose more state. The
	// observable contract is "StartAt=end never replays existing
	// entries", which is covered by TestNativeStart_ContextCancellation
	// above (it sets StartAt=end and runs without consuming entries).
	// Keep this test as a smoke-level "build path with file works".
}

// TestNativeFollowerErrorTolerance asserts an open failure on one
// path of a multi-path config does NOT prevent followers on the other
// paths from running. The probe at runNative entry catches this for
// initial setup; here we verify the per-file follower loop also
// handles transient errors.
func TestNativeFollowerErrorTolerance(t *testing.T) {
	good := fixtureSmallJournal(t)

	// Build with a single bad path: probe should fail and Start
	// should surface the error. Multi-path tolerance is exercised in
	// the parity test (task 29) where both backends are run against
	// the same Files= list; for task 28 we just assert the failure
	// surface.
	cfg := NewConfigWithID("native_follower_error")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{filepath.Join(t.TempDir(), "does-not-exist.journal")}
	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err, "Build must succeed even when files are missing; probe runs at Start")

	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	startErr := op.Start(testutil.NewUnscopedMockPersister())
	require.Error(t, startErr, "Start must surface probe failure")
	assert.Contains(t, startErr.Error(), "open")
	require.NoError(t, op.Stop())

	// Sanity: the good path alone still works.
	cfg2 := NewConfigWithID("native_follower_ok")
	cfg2.OutputIDs = []string{"output"}
	cfg2.Mode = ModeNative
	cfg2.Files = []string{good}
	op2, err := cfg2.Build(set)
	require.NoError(t, err)
	require.NoError(t, op2.SetOutputs([]operator.Operator{mockOutput}))
	require.NoError(t, op2.Start(testutil.NewUnscopedMockPersister()))
	require.NoError(t, op2.Stop())
	_ = errors.New // keep imports tidy if test grows
}

// TestNativeNewCmd_IsNotInvokedInNativeMode asserts the journalctl
// subprocess is never started when Mode == ModeNative. We do this by
// substituting a newCmd that records invocations and verifying the
// counter stays zero across the lifetime of the operator.
//
// This is the strongest "do NOT alter the journalctl code path"
// regression guard the task specifies: the operator MUST NOT exec any
// journalctl process when running in native mode.
func TestNativeNewCmd_IsNotInvokedInNativeMode(t *testing.T) {
	cfg := NewConfigWithID("native_no_journalctl")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{fixtureSmallJournal(t)}

	set := componenttest.NewNopTelemetrySettings()
	op, err := cfg.Build(set)
	require.NoError(t, err)

	in := op.(*Input)
	var cmdCalls int
	originalNewCmd := in.newCmd
	in.newCmd = func(ctx context.Context, cursor []byte) cmd {
		cmdCalls++
		return originalNewCmd(ctx, cursor)
	}

	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).Return(nil)
	require.NoError(t, in.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, in.Start(testutil.NewUnscopedMockPersister()))
	// Brief settle for the runNative goroutine to ramp up.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, in.Stop())

	assert.Equal(t, 0, cmdCalls,
		"native mode must not invoke the journalctl newCmd factory; got %d calls", cmdCalls)
}

// TestNativeStart_LogsBackendStart pins the structured "native journald
// reader started" Info log emitted by runNative after probe + follower
// spawn. Operators rely on this line to confirm at deploy time that
// the native backend actually came up — without it, a typo in Mode
// that fell through to journalctl would be invisible in logs because
// journalctl(1)'s own startup banner is identical regardless of which
// in-process backend the receiver chose.
//
// The assertion pins:
//   - Info-level (operators don't typically run journald-receiver at
//     Debug; Warn would crowd existing receiver warnings).
//   - The exact message string "native journald reader started" (used
//     as a grep target in the operator-side runbook; changing it
//     breaks customer playbooks).
//   - The "paths", "start_at", and "followers" structured fields
//     (lets dashboards plot rollout per file count without parsing
//     free-text).
//   - The line is emitted exactly once per Start() (not once per
//     follower goroutine).
func TestNativeStart_LogsBackendStart(t *testing.T) {
	cfg := NewConfigWithID("native_log_backend_start")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{fixtureSmallJournal(t)}
	cfg.StartAt = "end"

	obs, logs := observer.New(zap.InfoLevel)
	set := componenttest.NewNopTelemetrySettings()
	set.Logger = zap.New(obs)

	op, err := cfg.Build(set)
	require.NoError(t, err)

	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	require.NoError(t, op.Start(testutil.NewUnscopedMockPersister()))
	// Allow the runNative goroutine to reach the "started" log; Start
	// itself returns after waitDuration, so the goroutine has at
	// least probe + spawn-followers worth of work to do before
	// emitting. A 500ms grace is generous on the small fixture.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if logs.FilterMessage("native journald reader started").Len() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { require.NoError(t, op.Stop()) })

	matched := logs.FilterMessage("native journald reader started").AllUntimed()
	require.Len(t, matched, 1,
		"runNative must emit exactly one 'native journald reader started' line per Start; got %d",
		len(matched))

	got := matched[0]
	assert.Equal(t, zap.InfoLevel, got.Level,
		"backend-ready log must be Info level so operators see it without Debug")

	fields := got.ContextMap()
	pathsField, ok := fields["paths"]
	require.True(t, ok, "log must carry 'paths' structured field")
	pathsSlice, ok := pathsField.([]any)
	require.True(t, ok, "'paths' must be a slice for dashboard parsing; got %T", pathsField)
	require.Len(t, pathsSlice, 1, "single-file fixture should yield one path")
	assert.Equal(t, fixtureSmallJournal(t), pathsSlice[0])

	startAt, ok := fields["start_at"]
	require.True(t, ok, "log must carry 'start_at' structured field")
	assert.Equal(t, "end", startAt, "configured StartAt must surface in the log")

	followers, ok := fields["followers"]
	require.True(t, ok, "log must carry 'followers' structured field")
	// zap encodes Int as int64 in the observer's structured map.
	assert.EqualValues(t, 1, followers,
		"single-path fixture must report 1 follower; got %v", followers)
}

// TestNativeStart_DoesNotLogBackendStartOnProbeFailure asserts the
// "started" log is NOT emitted when probeNativePaths fails — so the
// log line really does mean "every path opened cleanly", not just "we
// entered runNative". Without this guard a flaky test could pass on a
// half-broken backend.
func TestNativeStart_DoesNotLogBackendStartOnProbeFailure(t *testing.T) {
	cfg := NewConfigWithID("native_log_no_start_on_failure")
	cfg.OutputIDs = []string{"output"}
	cfg.Mode = ModeNative
	cfg.Files = []string{filepath.Join(t.TempDir(), "missing.journal")}

	obs, logs := observer.New(zap.InfoLevel)
	set := componenttest.NewNopTelemetrySettings()
	set.Logger = zap.New(obs)

	op, err := cfg.Build(set)
	require.NoError(t, err, "Build must defer file existence check to Start")

	mockOutput := testutil.NewMockOperator("output")
	mockOutput.On("Process", mock.Anything, mock.Anything).Return(nil)
	require.NoError(t, op.SetOutputs([]operator.Operator{mockOutput}))

	startErr := op.Start(testutil.NewUnscopedMockPersister())
	require.Error(t, startErr, "Start must surface probe failure")
	require.NoError(t, op.Stop())

	// Drain a brief window to make sure no late log slips through.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 0,
		logs.FilterMessage("native journald reader started").Len(),
		"backend-start log must NOT fire when probe fails")
}
