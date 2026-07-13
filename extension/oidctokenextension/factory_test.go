// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package oidctokenextension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/extension/extensiontest"
)

func TestNewFactory(t *testing.T) {
	f := NewFactory()
	assert.NotNil(t, f)

	cfg := createDefaultConfig().(*Config)
	assert.Equal(t, f.CreateDefaultConfig().(*Config), cfg)

	ext, err := createExtension(t.Context(), extensiontest.NewNopSettings(f.Type()), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, ext)

	fext, err := f.Create(t.Context(), extensiontest.NewNopSettings(f.Type()), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, fext)

	// Both constructors must yield the same concrete type wrapping the same
	// config. Whole-object equality is avoided here: the extension embeds sync
	// primitives and channels that are not meaningfully comparable between two
	// independently-created instances.
	assert.IsType(t, ext, fext)
	assert.Same(t, cfg, ext.(*oidcTokenExtension).config)
	assert.Same(t, cfg, fext.(*oidcTokenExtension).config)
}

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	assert.Equal(t, &Config{Provider: ProviderAuto}, cfg)
	assert.NoError(t, componenttest.CheckConfigStruct(cfg))
}

func TestCreate(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	ext, err := createExtension(t.Context(), extensiontest.NewNopSettings(extensiontest.NopType), cfg)
	assert.NoError(t, err)
	assert.NotNil(t, ext)
}
