// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package journaldreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/journaldreceiver"

import (
	"fmt"

	"go.opentelemetry.io/collector/featuregate"
)

// nativeReaderGateID is the wire-level identifier of the alpha feature gate
// that opt-ins the in-process pure-Go journal reader implemented in
// pkg/stanza/operator/input/journald/native.
//
// The string is part of the operator-facing contract (operators set it via
// --feature-gates=+journaldreceiver.useNativeReader on the collector
// command line or via collector config), so it must not be edited
// without a deprecation cycle. The freeze test
// TestNativeReaderGateID covers accidental edits.
const nativeReaderGateID = "journaldreceiver.useNativeReader"

// useNativeReaderGate registers the alpha feature gate that gates the
// native binary journal reader. It is required (in addition to setting
// Mode=native on the receiver config) before the receiver will dispatch
// to the native backend.
//
// Stage rationale: StageAlpha. The native backend is brand new code with
// fuzz coverage but no production bake time, so it MUST be off by
// default and require an explicit opt-in. Promotion to Beta then Stable
// will happen in follow-up changes once we have run the parity test
// (task 29) and regression suite (task 30) clean and have collected
// real-world telemetry.
//
// Fail-closed semantics: if cfg.Mode == ModeNative but this gate is not
// enabled, JournaldConfig.Validate returns an error and receiver
// creation fails. There is no silent fallback to the journalctl backend
// because that would mask operator intent.
var useNativeReaderGate = featuregate.GlobalRegistry().MustRegister(
	nativeReaderGateID,
	featuregate.StageAlpha,
	featuregate.WithRegisterDescription(
		"When enabled together with mode: native, the journald receiver "+
			"reads the systemd journal binary format in-process via a "+
			"pure-Go reader instead of shelling out to journalctl(1). "+
			"This gate is alpha; the default journalctl backend is "+
			"unaffected. Setting mode: native without this gate "+
			"enabled is a configuration error (fail-closed).",
	),
	featuregate.WithRegisterReferenceURL(
		"https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/32711",
	),
)

// errNativeRequiresFeatureGate is the sentinel error returned by
// JournaldConfig.Validate when Mode is ModeNative but
// useNativeReaderGate is not enabled.
//
// The message is intentionally specific so an operator who set
// mode: native without flipping the gate sees the exact CLI flag they
// need rather than a generic "invalid mode". Tests pin the substrings
// "mode: native" and the gate ID so accidental message edits trip CI.
//
// Declared as a package-level var (rather than minted by a constructor
// on every failure) so callers may use errors.Is() to detect the
// fail-closed condition without string-matching, and so the diff for
// task 27 carries the exact registration-and-error pair that DoD-5
// freezes alongside the Validate dispatch in config.go.
var errNativeRequiresFeatureGate = fmt.Errorf(
	"journaldreceiver: mode: %q requires the %q feature gate to be "+
		"enabled (e.g. --feature-gates=+%s); see "+
		"https://github.com/open-telemetry/opentelemetry-collector-contrib/issues/32711",
	ModeNative, nativeReaderGateID, nativeReaderGateID,
)

// validateNativeBackend is the centralised fail-closed check called by
// JournaldConfig.Validate when Mode == ModeNative. It returns
// errNativeRequiresFeatureGate when the alpha gate is disabled and nil
// otherwise. Putting the check here (rather than inlining
// useNativeReaderGate.IsEnabled() in the receiver-config switch) keeps
// every gate-vs-mode decision next to the gate registration so a
// future stage promotion only has to touch this file.
func validateNativeBackend() error {
	if !useNativeReaderGate.IsEnabled() {
		return errNativeRequiresFeatureGate
	}
	return nil
}

// nativeReaderEnabled reports whether the native backend should be used
// for the supplied config. It returns true only when Mode is ModeNative
// AND the alpha feature gate is enabled. Callers in the input operator
// (task 28) and the parity test (task 29) MUST consult this helper
// rather than reading cfg.Mode directly so the fail-closed contract is
// applied uniformly.
func nativeReaderEnabled(cfg *JournaldConfig) bool {
	if cfg == nil {
		return false
	}
	return cfg.Mode == ModeNative && useNativeReaderGate.IsEnabled()
}
