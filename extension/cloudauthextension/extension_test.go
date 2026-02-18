// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cloudauthextension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStartWithTokenFile(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("test-token"), 0o600))

	cfg := &Config{TokenFile: tokenFile}
	ext := &cloudAuthExtension{
		logger: zap.NewNop(),
		config: cfg,
	}

	err := ext.Start(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, tokenFile, os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE"))
}

func TestStartWithoutProvider(t *testing.T) {
	cfg := &Config{}
	ext := &cloudAuthExtension{
		logger: zap.NewNop(),
		config: cfg,
	}

	err := ext.Start(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no OIDC provider detected")
}

func TestShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	tokenFile := filepath.Join(tmpDir, "cloudauth-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("test"), 0o600))

	ext := &cloudAuthExtension{
		logger:    zap.NewNop(),
		tokenFile: tokenFile,
		done:      make(chan struct{}),
	}

	err := ext.Shutdown(t.Context())
	require.NoError(t, err)
	_, err = os.Stat(tokenFile)
	require.True(t, os.IsNotExist(err))
}

func TestFactory(t *testing.T) {
	factory := NewFactory()
	require.NotNil(t, factory)

	cfg := factory.CreateDefaultConfig()
	require.NotNil(t, cfg)
	require.IsType(t, &Config{}, cfg)
}
