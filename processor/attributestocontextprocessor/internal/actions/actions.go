// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package actions // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"

import (
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

func NewActions(keyValues []KeyValue) Actions {
	return Actions{actions: keyValues}
}

// ProcessResource copies configured resource attributes into the metadata map.
// Keys are lowercased to match client.NewMetadata behavior.
// - https://github.com/open-telemetry/opentelemetry-collector/blob/client/v1.30.0/client/client.go#L146
func (a *Actions) ProcessResource(metadata map[string][]string, attrs pcommon.Map) {
	for _, action := range a.actions {
		if val, found := attrs.Get(action.FromResourceAttribute); found {
			metadata[strings.ToLower(action.Key)] = []string{val.AsString()}
		}
	}
}
