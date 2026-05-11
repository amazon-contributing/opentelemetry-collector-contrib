// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestProcessResource(t *testing.T) {
	a := NewActions([]KeyValue{
		{Key: "service", FromResourceAttribute: "service.name"},
		{Key: "host", FromResourceAttribute: "host.name"},
	})

	attrs := pcommon.NewMap()
	attrs.PutStr("service.name", "my-service")
	attrs.PutStr("host.name", "my-host")

	metadata := make(map[string][]string)
	a.ProcessResource(metadata, attrs)

	assert.Equal(t, []string{"my-service"}, metadata["service"])
	assert.Equal(t, []string{"my-host"}, metadata["host"])
}

func TestProcessResource_OverwritesExisting(t *testing.T) {
	a := NewActions([]KeyValue{
		{Key: "service", FromResourceAttribute: "service.name"},
	})

	attrs := pcommon.NewMap()
	attrs.PutStr("service.name", "new-service")

	metadata := map[string][]string{
		"service": {"old-service"},
	}
	a.ProcessResource(metadata, attrs)

	assert.Equal(t, []string{"new-service"}, metadata["service"])
}

func TestProcessResource_KeyNormalizedToLowercase(t *testing.T) {
	a := NewActions([]KeyValue{
		{Key: "CWLogs.Log_Group", FromResourceAttribute: "cwlogs.log_group"},
	})

	attrs := pcommon.NewMap()
	attrs.PutStr("cwlogs.log_group", "/my/group")

	metadata := make(map[string][]string)
	a.ProcessResource(metadata, attrs)

	assert.Equal(t, []string{"/my/group"}, metadata["cwlogs.log_group"])
	assert.Empty(t, metadata["CWLogs.Log_Group"], "original casing should not exist")
}

func TestProcessResource_MissingAttribute(t *testing.T) {
	a := NewActions([]KeyValue{
		{Key: "missing", FromResourceAttribute: "not.found"},
	})

	attrs := pcommon.NewMap()
	metadata := make(map[string][]string)
	a.ProcessResource(metadata, attrs)

	assert.Empty(t, metadata)
}
