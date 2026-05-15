// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configopaque"
)

func TestPassfileResolution(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("localhost:5432:testdb:testuser:testpass\n"), 0o600))

	pw, err := resolvePasswordFromPassfile(f, "localhost:5432", "testdb", "testuser")
	require.NoError(t, err)
	require.Equal(t, "testpass", pw)
}

func TestPassfileWildcard(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("*:*:*:testuser:wildcardpass\n"), 0o600))

	pw, err := resolvePasswordFromPassfile(f, "anyhost:9999", "anydb", "testuser")
	require.NoError(t, err)
	require.Equal(t, "wildcardpass", pw)
}

func TestPassfileNoMatch(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("otherhost:5432:otherdb:otheruser:pass\n"), 0o600))

	_, err := resolvePasswordFromPassfile(f, "localhost:5432", "testdb", "testuser")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no matching entry in passfile")
}

func TestPassfileFirstMatchWins(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	content := "localhost:5432:mydb:testuser:firstpass\nlocalhost:5432:mydb:testuser:secondpass\n"
	require.NoError(t, os.WriteFile(f, []byte(content), 0o600))

	pw, err := resolvePasswordFromPassfile(f, "localhost:5432", "mydb", "testuser")
	require.NoError(t, err)
	require.Equal(t, "firstpass", pw)
}

func TestPassfileBadEndpoint(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("localhost:5432:db:user:pass\n"), 0o600))

	_, err := resolvePasswordFromPassfile(f, "no-port", "db", "user")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse endpoint")
}

func TestPasswordResolutionInlinePriority(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("localhost:5432:testdb:testuser:filepass\n"), 0o600))

	cfg := &Config{
		Username: "testuser",
		Password: configopaque.String("inlinepass"),
		Passfile: f,
		AddrConfig: confignet.AddrConfig{
			Endpoint:  "localhost:5432",
			Transport: confignet.TransportTypeTCP,
		},
	}

	factory := newDefaultClientFactory(cfg)
	// When password is set inline, passfile should not be consulted
	require.Equal(t, "inlinepass", factory.baseConfig.password)
	require.Equal(t, f, factory.passfile)
}

func TestDefaultClientFactoryPassfile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("localhost:5432:*:fileuser:filepass\n"), 0o600))

	cfg := &Config{
		Username: "fileuser",
		Passfile: f,
		AddrConfig: confignet.AddrConfig{
			Endpoint:  "localhost:5432",
			Transport: confignet.TransportTypeTCP,
		},
	}

	factory := newDefaultClientFactory(cfg)
	require.Equal(t, "fileuser", factory.baseConfig.username)
	require.Equal(t, "", factory.baseConfig.password)
	require.Equal(t, f, factory.passfile)
}

func TestPoolClientFactoryPassfile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(f, []byte("localhost:5432:*:pooluser:poolpass\n"), 0o600))

	cfg := &Config{
		Username: "pooluser",
		Passfile: f,
		AddrConfig: confignet.AddrConfig{
			Endpoint:  "localhost:5432",
			Transport: confignet.TransportTypeTCP,
		},
	}

	factory := newPoolClientFactory(cfg)
	require.Equal(t, "pooluser", factory.baseConfig.username)
	require.Equal(t, "", factory.baseConfig.password)
	require.Equal(t, f, factory.passfile)
}
