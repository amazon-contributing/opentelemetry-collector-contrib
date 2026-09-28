// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Mode config test coverage (cover both modes and invalid mode rejection):
//
//   - "cover both modes" -> TestConfigDefaultMode pins the
//     createDefaultConfig() path to ModeJournalctl, the documented
//     default. TestConfigValidate_AcceptsJournalctl pins the explicit-
//     default YAML path (operators who write "mode: journalctl"
//     literally). TestConfigValidate_AcceptsNative pins the opt-in YAML
//     path; mode: native is selected by config alone (no feature gate).
//   - "and invalid mode rejection" -> TestConfigValidate_RejectsInvalid
//     is table-driven over five hostile inputs that an operator might
//     plausibly type: an arbitrary garbage string, a capitalised
//     "Native", an upper-cased "JOURNALCTL", the older "journald"
//     synonym, and a whitespace-padded " native ". Each subtest asserts
//     err is non-nil, that the error contains "invalid mode", that it
//     echoes the bad value verbatim (so logs are debuggable), and that
//     it lists BOTH ModeJournalctl and ModeNative as valid
//     alternatives. TestConfigValidate_EmptyDefaultsToJournalctl covers
//     the empty-Mode normalisation path documented in config.go's
//     Validate().

package journaldreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/journaldreceiver"

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
// ModeJournalctl. The default must preserve current behavior exactly,
// which means new configs continue to dispatch to the journalctl
// subprocess backend.
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

// TestConfigValidate_AcceptsNative pins the native-backend opt-in path:
// mode: native must validate at the receiver-config layer. The native
// backend is selected by config alone; there is no feature gate.
func TestConfigValidate_AcceptsNative(t *testing.T) {
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
