// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package journald // import "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/input/journald"

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/stanza/operator/helper"
)

const operatorType = "journald_input"

// Backend selector values for Config.Mode. Kept in sync with
// the receiver-level constants in receiver/journaldreceiver/config.go
// (TestConfigModeConstants there freezes the wire-level strings).
//
// The operator package owns the canonical values because the dispatch
// site lives here (input.go / input_native.go); the receiver imports
// the operator, so re-rooting the strings on this side keeps the
// dependency graph acyclic.
const (
	// ModeJournalctl is the default backend: invoke journalctl(1) as a
	// subprocess and stream JSON entries from its stdout. This preserves
	// the receiver's historical behavior exactly.
	ModeJournalctl = "journalctl"

	// ModeNative selects the in-process pure-Go binary journal reader
	// implemented in pkg/stanza/operator/input/journald/native. The
	// backend is selected by config alone (the receiver's Validate()
	// accepts mode: native directly and rejects unrecognized values);
	// the operator-level dispatch in input.go branches on this value.
	ModeNative = "native"
)

// NewConfig creates a new input config with default values
func NewConfig() *Config {
	return NewConfigWithID(operatorType)
}

// NewConfigWithID creates a new input config with default values
func NewConfigWithID(operatorID string) *Config {
	return &Config{
		InputConfig: helper.NewInputConfig(operatorID, operatorType),
		StartAt:     "end",
		Priority:    "info",
	}
}

// Config is the configuration of a journald input operator
type Config struct {
	helper.InputConfig `mapstructure:",squash"`

	Directory           *string       `mapstructure:"directory,omitempty"`
	Files               []string      `mapstructure:"files,omitempty"`
	StartAt             string        `mapstructure:"start_at,omitempty"`
	Units               []string      `mapstructure:"units,omitempty"`
	Priority            string        `mapstructure:"priority,omitempty"`
	Matches             []MatchConfig `mapstructure:"matches,omitempty"`
	Identifiers         []string      `mapstructure:"identifiers,omitempty"`
	Grep                string        `mapstructure:"grep,omitempty"`
	Dmesg               bool          `mapstructure:"dmesg,omitempty"`
	All                 bool          `mapstructure:"all,omitempty"`
	Namespace           string        `mapstructure:"namespace,omitempty"`
	ConvertMessageBytes bool          `mapstructure:"convert_message_bytes,omitempty"`

	// Mode selects which backend reads the journal: ModeJournalctl
	// (default) or ModeNative. Set programmatically by the receiver's
	// InputConfig hook from JournaldConfig.Mode after Validate has
	// approved the value; the YAML "mode" key is owned by the
	// receiver-level config and must not be parsed here as well —
	// having two squashed "mode" tags at the same level would race and
	// the resulting decode order is undefined. The mapstructure:"-"
	// tag pins this contract.
	Mode string `mapstructure:"-"`
}

type MatchConfig map[string]string
