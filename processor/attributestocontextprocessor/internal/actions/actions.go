// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package actions // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"

import (
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// KeyValue specifies a resource attribute to copy to client metadata.
type KeyValue struct {
	Key                   string `mapstructure:"key"`
	FromResourceAttribute string `mapstructure:"from_resource_attribute"`
}

// Actions copies resource attributes to client metadata.
type Actions struct {
	actions []KeyValue
}

// NewActions creates Actions with keys pre-normalized to lowercase to match client.NewMetadata behavior.
// - https://github.com/open-telemetry/opentelemetry-collector/blob/client/v1.30.0/client/client.go#L146
func NewActions(keyValues []KeyValue) Actions {
	normalized := make([]KeyValue, len(keyValues))
	for i, kv := range keyValues {
		normalized[i] = KeyValue{Key: strings.ToLower(kv.Key), FromResourceAttribute: kv.FromResourceAttribute}
	}
	return Actions{actions: normalized}
}

// ProcessResource copies configured resource attributes into the metadata map.
func (a *Actions) ProcessResource(metadata map[string][]string, attrs pcommon.Map) {
	for _, action := range a.actions {
		if val, found := attrs.Get(action.FromResourceAttribute); found {
			metadata[action.Key] = []string{val.AsString()}
		}
	}
}

const groupKeySeparator = '|'

// GroupKey returns a deterministic string key for the metadata values in config order.
// Values are %q-quoted so the separator cannot cause collisions.
func (a *Actions) GroupKey(metadata map[string][]string) string {
	var b strings.Builder
	for i, action := range a.actions {
		if i > 0 {
			b.WriteByte(groupKeySeparator)
		}
		b.WriteString(action.Key)
		b.WriteByte('=')
		_, _ = fmt.Fprintf(&b, "%q", metadata[action.Key])
	}
	return b.String()
}
