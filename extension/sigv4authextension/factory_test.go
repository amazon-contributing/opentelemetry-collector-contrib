// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sigv4authextension

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/extension/extensiontest"
)

func TestNewFactory(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join("testdata", "credentials"))

	f := NewFactory()
	assert.NotNil(t, f)

	cfg := createDefaultConfig().(*Config)
	assert.Equal(t, f.CreateDefaultConfig().(*Config), cfg)
	cfg.Region = "us-east-1"

	ext, err := createExtension(t.Context(), extensiontest.NewNopSettings(f.Type()), cfg)
	require.NoError(t, err)
	require.NotNil(t, ext)

	fext, err := f.Create(t.Context(), extensiontest.NewNopSettings(f.Type()), cfg)
	require.NoError(t, err)
	require.NotNil(t, fext)
}

func TestCreateDefaultConfig(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	assert.Equal(t, &Config{}, cfg)
	assert.NoError(t, componenttest.CheckConfigStruct(cfg))
}

func TestCreate(t *testing.T) {
	isolateAWSEnv(t)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join("testdata", "credentials"))

	cfg := createDefaultConfig().(*Config)
	cfg.Region = "us-east-1"

	ext, err := createExtension(t.Context(), extensiontest.NewNopSettings(extensiontest.NopType), cfg)
	require.NoError(t, err)
	require.NotNil(t, ext)
}
