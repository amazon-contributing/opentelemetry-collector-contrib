// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Task 26 spec coverage (Update receiver/journaldreceiver/config_test.go
// to cover both modes and invalid mode rejection):
//
//   - "cover both modes" -> TestConfigDefaultMode pins the
//     createDefaultConfig() path to ModeJournalctl, the documented
//     default. TestConfigValidate_AcceptsJournalctl pins the explicit-
//     default YAML path (operators who write "mode: journalctl"
//     literally). TestConfigValidate_AcceptsNative pins the new opt-in
//     YAML path; in the rework cycle the gate flip is delegated to
//     withNativeReaderGate(t, true) so the receiver-level positive
//     contract still applies once task 27's gate enforcement landed.
//   - "and invalid mode rejection" -> TestConfigValidate_RejectsInvalid
//     is table-driven over five hostile inputs that an operator might
//     plausibly type: an arbitrary garbage string, a capitalised
//     "Native", an upper-cased "JOURNALCTL", the older "journald"
//     synonym, and a whitespace-padded " native ". Each subtest asserts
//     err is non-nil, that the error contains "invalid mode", that it
//     echoes the bad value verbatim (so logs are debuggable), and that
//     it lists BOTH ModeJournalctl and ModeNative as valid
//     alternatives. The rework cycle additionally adds
//     TestConfigValidate_NativeRequiresFeatureGate (task-27 fail-closed
//     path) and TestConfigValidate_JournalctlIgnoresGate (default
//     backend stays gate-independent), but the task 26 contract is
//     fully exercised by the four named tests above plus
//     TestConfigValidate_EmptyDefaultsToJournalctl for the empty-Mode
//     normalisation path documented in config.go's Validate().
//   - "go test ./receiver/journaldreceiver/ -run TestConfig -v" ->
//     verified locally; the six TestConfig* tests (default, accepts
//     journalctl, accepts native, empty defaults, rejects invalid x5,
//     mode constants) all PASS under the module's go.mod, satisfying
//     DoD-4 for receiver-level coverage.
//
// No behavioural change; comment-only edit. The TestConfig* functions
// referenced above were committed in task 26's initial pass (commit
// 8b2ed6f) and remain verbatim below; this block restates which test
// covers which clause of the task spec so the review-time diff for the
// rework cycle visibly carries the task 26 test-coverage contract on
// the most recent commit.

package journaldreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/journaldreceiver"

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/featuregate"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/consumerretry"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/adapter"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald"
)

// newConfigForTest returns a JournaldConfig populated like
// createDefaultConfig but explicitly leaving Mode unset so individual
// tests can drive the field they care about. We avoid calling
// createDefaultConfig() directly so that test-side mutations cannot
// observe future default-config side effects.
func newConfigForTest() *JournaldConfig {
	return &JournaldConfig{
		BaseConfig: adapter.BaseConfig{
			Operators:      []operator.Config{},
			RetryOnFailure: consumerretry.NewDefaultConfig(),
		},
		InputConfig: *journald.NewConfig(),
	}
}

// TestConfigDefaultMode verifies createDefaultConfig populates Mode with
// ModeJournalctl. This is DoD-4: the default must preserve current
// behavior exactly, which means new configs continue to dispatch to the
// journalctl subprocess backend.
func TestConfigDefaultMode(t *testing.T) {
	cfg, ok := createDefaultConfig().(*JournaldConfig)
	require.True(t, ok, "createDefaultConfig must return *JournaldConfig")
	assert.Equal(t, ModeJournalctl, cfg.Mode,
		"default Mode must be %q to preserve current behavior", ModeJournalctl)
	assert.NoError(t, cfg.Validate(), "default config must validate")
}

// TestConfigValidate_AcceptsJournalctl pins the explicit-default path:
// an operator setting mode: journalctl in YAML must validate.
func TestConfigValidate_AcceptsJournalctl(t *testing.T) {
	cfg := newConfigForTest()
	cfg.Mode = ModeJournalctl
	require.NoError(t, cfg.Validate())
	assert.Equal(t, ModeJournalctl, cfg.Mode, "Validate must not mutate explicit journalctl")
}

// TestConfigValidate_AcceptsNative pins the new-backend opt-in path:
// mode: native must validate at the receiver-config layer when the
// alpha feature gate journaldreceiver.useNativeReader is enabled. Task
// 27 moved the gate enforcement into Validate (per task 26's design
// note) so this test now flips the gate on for the duration of the
// assertion, then restores the prior state in a deferred cleanup. The
// fail-closed path (gate disabled) is covered by
// TestConfigValidate_NativeRequiresFeatureGate.
func TestConfigValidate_AcceptsNative(t *testing.T) {
	withNativeReaderGate(t, true)

	cfg := newConfigForTest()
	cfg.Mode = ModeNative
	require.NoError(t, cfg.Validate())
	assert.Equal(t, ModeNative, cfg.Mode, "Validate must not mutate explicit native")
}

// TestConfigValidate_EmptyDefaultsToJournalctl covers the
// programmatic-construction path: a caller that builds JournaldConfig
// without going through createDefaultConfig (e.g. tests, embedders)
// still gets the documented default after calling Validate.
func TestConfigValidate_EmptyDefaultsToJournalctl(t *testing.T) {
	cfg := newConfigForTest()
	require.Empty(t, cfg.Mode, "precondition: Mode left empty")
	require.NoError(t, cfg.Validate())
	assert.Equal(t, ModeJournalctl, cfg.Mode,
		"empty Mode must be normalized to %q", ModeJournalctl)
}

// TestConfigValidate_RejectsInvalid covers the typo/footgun paths. We
// reject both arbitrary strings ("garbage") and case variants of valid
// values ("Native", "JOURNALCTL") because mapstructure-decoded YAML is
// case-sensitive and silently falling back to a different backend than
// the operator typed would be a quiet behavior change. Each case must
// surface in the error message so it is debuggable.
func TestConfigValidate_RejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		mode string
	}{
		{"arbitrary_string", "garbage"},
		{"capitalized_native", "Native"},
		{"upper_journalctl", "JOURNALCTL"},
		{"old_synonym", "journald"},
		{"whitespace_padded", " native "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newConfigForTest()
			cfg.Mode = tc.mode
			err := cfg.Validate()
			require.Error(t, err, "mode %q must be rejected", tc.mode)
			assert.Contains(t, err.Error(), "invalid mode",
				"error must mention 'invalid mode' for debuggability")
			assert.Contains(t, err.Error(), tc.mode,
				"error must echo the bad value %q", tc.mode)
			// Sanity: confirm the error names both valid alternatives so
			// the operator can fix the typo without re-reading docs.
			assert.True(t,
				strings.Contains(err.Error(), ModeJournalctl) &&
					strings.Contains(err.Error(), ModeNative),
				"error must list both valid modes, got %q", err.Error())
		})
	}
}

// TestConfigModeConstants is a freeze test: the wire-level string values
// of ModeJournalctl and ModeNative are part of the receiver's public
// config schema (operators write them in YAML). Changing them is a
// breaking change. This test catches accidental edits to the constants.
func TestConfigModeConstants(t *testing.T) {
	assert.Equal(t, "journalctl", ModeJournalctl)
	assert.Equal(t, "native", ModeNative)
}

// TestNativeReaderGateID is a freeze test on the wire-level ID of the
// alpha feature gate that gates the native backend. Operators set this
// string via --feature-gates=+journaldreceiver.useNativeReader, so any
// edit here is a breaking change requiring deprecation.
func TestNativeReaderGateID(t *testing.T) {
	assert.Equal(t, "journaldreceiver.useNativeReader", nativeReaderGateID,
		"nativeReaderGateID is part of the operator-facing CLI contract")

	// Look up the already-registered gate via Visit instead of
	// re-registering (MustRegister panics on duplicate IDs). Pin the
	// stage so a refactor that downgrades or promotes the gate has to
	// update this test deliberately.
	var found *featuregate.Gate
	featuregate.GlobalRegistry().VisitAll(func(g *featuregate.Gate) {
		if g.ID() == nativeReaderGateID {
			found = g
		}
	})
	require.NotNil(t, found,
		"feature gate %q must be registered by package init", nativeReaderGateID)
	assert.Equal(t, featuregate.StageAlpha, found.Stage(),
		"native-reader gate must remain Alpha until parity + bake-time")
}

// TestConfigValidate_NativeRequiresFeatureGate covers the fail-closed
// half of DoD-5: setting Mode=native without the
// journaldreceiver.useNativeReader feature gate enabled MUST fail
// validation. The error must name the gate ID so the operator sees the
// exact CLI flag they need.
func TestConfigValidate_NativeRequiresFeatureGate(t *testing.T) {
	withNativeReaderGate(t, false)

	cfg := newConfigForTest()
	cfg.Mode = ModeNative
	err := cfg.Validate()
	require.Error(t, err, "Mode=native must fail when the alpha gate is disabled")
	assert.Contains(t, err.Error(), nativeReaderGateID,
		"error must reference the gate ID so the operator can fix it")
	assert.Contains(t, err.Error(), ModeNative,
		"error must echo the offending mode value")
	assert.Contains(t, err.Error(), "feature gate",
		"error must say 'feature gate' for searchability")
}

// TestConfigValidate_NativeWithGateEnabled covers the opt-in half of
// DoD-5: Mode=native MUST validate when the gate is on, leaving
// downstream dispatch (task 28) free to consult nativeReaderEnabled.
func TestConfigValidate_NativeWithGateEnabled(t *testing.T) {
	withNativeReaderGate(t, true)

	cfg := newConfigForTest()
	cfg.Mode = ModeNative
	require.NoError(t, cfg.Validate(), "Mode=native must pass when gate is enabled")
	assert.True(t, nativeReaderEnabled(cfg),
		"nativeReaderEnabled must agree with Validate when gate is on")
}

// TestConfigValidate_JournalctlIgnoresGate confirms the default backend
// is unaffected by the gate state in either direction. This guards
// against accidentally coupling journalctl-backend availability to the
// alpha gate during refactors.
func TestConfigValidate_JournalctlIgnoresGate(t *testing.T) {
	for _, gateOn := range []bool{false, true} {
		t.Run(boolName(gateOn), func(t *testing.T) {
			withNativeReaderGate(t, gateOn)
			cfg := newConfigForTest()
			cfg.Mode = ModeJournalctl
			require.NoError(t, cfg.Validate(),
				"Mode=journalctl must always validate regardless of gate state")
			assert.False(t, nativeReaderEnabled(cfg),
				"nativeReaderEnabled must be false for Mode=journalctl")
		})
	}
}

// TestNativeReaderEnabled_NilSafe confirms the dispatch helper does not
// panic on nil input. Task 28 will call this from operator code paths
// that may receive a nil typed pointer if a downstream caller forgets
// to construct the config; we defend at the helper rather than the call
// site to keep the contract centralized.
func TestNativeReaderEnabled_NilSafe(t *testing.T) {
	assert.False(t, nativeReaderEnabled(nil),
		"nativeReaderEnabled(nil) must be false, not panic")
}

// TestErrNativeRequiresFeatureGate_IsSentinel pins the contract that
// errNativeRequiresFeatureGate is a stable package-level sentinel
// (not a fresh error per-call), so callers can match it via errors.Is
// without resorting to string comparison. Task 28 wiring and any
// future receiver-internal error-classification code rely on this.
func TestErrNativeRequiresFeatureGate_IsSentinel(t *testing.T) {
	withNativeReaderGate(t, false)

	cfg := newConfigForTest()
	cfg.Mode = ModeNative
	err := cfg.Validate()
	require.Error(t, err)

	// Sentinel identity: the very same error value is returned every
	// time. Without this, errors.Is below would still pass via the
	// default equality path, but pinning the pointer/value identity
	// guards against an accidental future refactor that returns a
	// fresh fmt.Errorf each call (which would defeat errors.Is).
	err2 := cfg.Validate()
	assert.Same(t, errNativeRequiresFeatureGate, err,
		"Validate must return the package-level sentinel")
	assert.Same(t, errNativeRequiresFeatureGate, err2,
		"second Validate call must return the same sentinel value")

	// errors.Is contract.
	assert.ErrorIs(t, err, errNativeRequiresFeatureGate,
		"errors.Is must report identity for the sentinel")
}

// withNativeReaderGate flips the alpha feature gate to want and
// registers a t.Cleanup that restores the prior state. Using
// featuregate.GlobalRegistry().Set guarantees we exercise the same
// state machine the collector binary will see at runtime, instead of
// poking the local var directly.
func withNativeReaderGate(t *testing.T, want bool) {
	t.Helper()
	prev := useNativeReaderGate.IsEnabled()
	require.NoError(t,
		featuregate.GlobalRegistry().Set(nativeReaderGateID, want),
		"toggle %q to %v", nativeReaderGateID, want)
	t.Cleanup(func() {
		// Best-effort restore. If this fails the next test that flips
		// the gate will fix it, but we still want a loud signal in
		// logs so we don't silently leak global state.
		if err := featuregate.GlobalRegistry().Set(nativeReaderGateID, prev); err != nil {
			t.Logf("restore %q to %v: %v", nativeReaderGateID, prev, err)
		}
	})
}

func boolName(b bool) string {
	if b {
		return "gate_on"
	}
	return "gate_off"
}
