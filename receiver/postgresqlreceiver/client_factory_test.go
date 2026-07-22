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
	require.Equal(t, "inlinepass", factory.baseConfig.password)

	// When password is set, passfile should not appear in connection string
	connStr, err := factory.baseConfig.ConnectionString()
	require.NoError(t, err)
	require.NotContains(t, connStr, "passfile")
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
	require.Empty(t, factory.baseConfig.password)
	require.Equal(t, f, factory.baseConfig.passfile)
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
	require.Empty(t, factory.baseConfig.password)
	require.Equal(t, f, factory.baseConfig.passfile)
}

func TestConnectionStringWithPassfile(t *testing.T) {
	cfg := postgreSQLConfig{
		username: "testuser",
		passfile: "/tmp/pgpass",
		address: confignet.AddrConfig{
			Endpoint:  "localhost:5432",
			Transport: confignet.TransportTypeTCP,
		},
	}

	connStr, err := cfg.ConnectionString()
	require.NoError(t, err)
	require.Contains(t, connStr, "passfile='/tmp/pgpass'")
}

func TestConnectionStringWithoutPassfile(t *testing.T) {
	cfg := postgreSQLConfig{
		username: "testuser",
		password: "testpass",
		address: confignet.AddrConfig{
			Endpoint:  "localhost:5432",
			Transport: confignet.TransportTypeTCP,
		},
	}

	connStr, err := cfg.ConnectionString()
	require.NoError(t, err)
	require.NotContains(t, connStr, "passfile")
}
