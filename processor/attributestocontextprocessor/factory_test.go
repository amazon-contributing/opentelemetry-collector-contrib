// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap/xconfmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/metadata"
)

func TestFactory_Type(t *testing.T) {
	factory := NewFactory()
	assert.Equal(t, metadata.Type, factory.Type())
}

func TestFactory_CreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	assert.NotNil(t, cfg)
	assert.NoError(t, componenttest.CheckConfigStruct(cfg))

	config := cfg.(*Config)
	assert.Empty(t, config.Actions)
}

func TestValidateConfig(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()
	assert.Error(t, xconfmap.Validate(cfg))
}

func TestFactory_CreateLogs(t *testing.T) {
	factory := NewFactory()
	cfg := &Config{
		Actions: []actions.KeyValue{
			{Key: "service", FromResourceAttribute: "service.name"},
		},
	}

	processor, err := factory.CreateLogs(
		t.Context(),
		processortest.NewNopSettings(metadata.Type),
		cfg,
		consumertest.NewNop(),
	)

	assert.NoError(t, err)
	assert.NotNil(t, processor)
}
