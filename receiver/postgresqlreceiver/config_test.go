// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver/internal/metadata"
)

func TestValidate(t *testing.T) {
	testCases := []struct {
		desc                  string
		defaultConfigModifier func(cfg *Config)
		expected              []error
	}{
		{
			desc:                  "missing username and password",
			defaultConfigModifier: func(*Config) {},
			expected: []error{
				errors.New(ErrNoUsername),
				errors.New(ErrNoPassword),
			},
		},
		{
			desc: "missing password",
			defaultConfigModifier: func(cfg *Config) {
				cfg.Username = "otel"
			},
			expected: []error{
				errors.New(ErrNoPassword),
			},
		},
		{
			desc: "missing username",
			defaultConfigModifier: func(cfg *Config) {
				cfg.Password = "otel"
			},
			expected: []error{
				errors.New(ErrNoUsername),
			},
		},
		{
			desc: "bad endpoint",
			defaultConfigModifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Password = "otel"
				cfg.Endpoint = "open-telemetry"
			},
			expected: []error{
				errors.New(ErrHostPort),
			},
		},
		{
			desc: "bad transport",
			defaultConfigModifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Password = "otel"
				cfg.Transport = "udp"
			},
			expected: []error{
				errors.New(ErrTransportsSupported),
			},
		},
		{
			desc: "unsupported SSL params",
			defaultConfigModifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Password = "otel"
				cfg.ServerName = "notlocalhost"
				cfg.MinVersion = "1.0"
				cfg.MaxVersion = "1.0"
			},
			expected: []error{
				fmt.Errorf(ErrNotSupported, "ServerName"),
				fmt.Errorf(ErrNotSupported, "MaxVersion"),
				fmt.Errorf(ErrNotSupported, "MinVersion"),
			},
		},
		{
			desc: "no error",
			defaultConfigModifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Password = "otel"
			},
			expected: nil,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			factory := NewFactory()
			cfg := factory.CreateDefaultConfig().(*Config)
			tC.defaultConfigModifier(cfg)
			actual := xconfmap.Validate(cfg)
			if len(tC.expected) > 0 {
				for _, err := range tC.expected {
					require.ErrorContains(t, actual, err.Error())
				}
			}
		})
	}
}

func TestPassfileValidation(t *testing.T) {
	validFile := filepath.Join(t.TempDir(), "pgpass")
	require.NoError(t, os.WriteFile(validFile, []byte("localhost:5432:testdb:otel:secret\n"), 0o600))

	validFileReadOnly := filepath.Join(t.TempDir(), "pgpass_ro")
	require.NoError(t, os.WriteFile(validFileReadOnly, []byte("localhost:5432:testdb:otel:secret\n"), 0o400))

	badPermsFile := filepath.Join(t.TempDir(), "pgpass_bad")
	require.NoError(t, os.WriteFile(badPermsFile, []byte("localhost:5432:*:otel:secret\n"), 0o644)) //nolint:gosec

	factory := NewFactory()

	testCases := []struct {
		desc        string
		modifier    func(cfg *Config)
		expectError string
	}{
		{
			desc: "username + passfile with valid pgpass content - valid",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Passfile = validFile
			},
		},
		{
			desc: "username + passfile with 0400 permissions - valid",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Passfile = validFileReadOnly
			},
		},
		{
			desc: "username + inline password (no file) - valid",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Password = "inline"
			},
		},
		{
			desc: "username + password + passfile - valid (password takes priority)",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Password = "inline"
				cfg.Passfile = "/nonexistent" // doesn't matter when inline password set
			},
		},
		{
			desc: "no username - error",
			modifier: func(cfg *Config) {
				cfg.Password = "otel"
			},
			expectError: ErrNoUsername,
		},
		{
			desc: "no password and no passfile - error",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
			},
			expectError: ErrNoPassword,
		},
		{
			desc: "passfile nonexistent - error",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Passfile = "/nonexistent/path/pgpass"
			},
			expectError: "`passfile` is inaccessible",
		},
		{
			desc: "passfile wrong permissions - error (linux only)",
			modifier: func(cfg *Config) {
				cfg.Username = "otel"
				cfg.Passfile = badPermsFile
			},
			expectError: func() string {
				if runtime.GOOS == "linux" {
					return "permissions must be 0600 or 0400"
				}
				return ""
			}(),
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			cfg := factory.CreateDefaultConfig().(*Config)
			tC.modifier(cfg)
			actual := xconfmap.Validate(cfg)
			if tC.expectError != "" {
				require.ErrorContains(t, actual, tC.expectError)
			} else if actual != nil {
				// May still have unrelated errors (e.g. endpoint), just ensure no password/username errors
				require.NotContains(t, actual.Error(), "password")
				require.NotContains(t, actual.Error(), "username")
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	cm, confErr := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, confErr)

	factory := NewFactory()
	cfg := factory.CreateDefaultConfig()

	t.Run("postgresql/minimal", func(t *testing.T) {
		sub, err := cm.Sub(component.NewIDWithName(metadata.Type, "minimal").String())
		require.NoError(t, err)
		require.NoError(t, sub.Unmarshal(cfg))

		expected := factory.CreateDefaultConfig().(*Config)
		expected.Endpoint = "localhost:5432"
		expected.Username = "otel"
		expected.Password = "${env:POSTGRESQL_PASSWORD}"
		//nolint:staticcheck
		expected.QuerySampleCollection.Enabled = true
		require.Equal(t, expected, cfg)
	})

	cfg = factory.CreateDefaultConfig()

	t.Run("postgresql/pool", func(t *testing.T) {
		sub, err := cm.Sub(component.NewIDWithName(metadata.Type, "pool").String())
		require.NoError(t, err)
		require.NoError(t, sub.Unmarshal(cfg))

		expected := factory.CreateDefaultConfig().(*Config)
		expected.Endpoint = "localhost:5432"
		expected.Transport = confignet.TransportTypeTCP
		expected.Username = "otel"
		expected.Password = "${env:POSTGRESQL_PASSWORD}"
		expected.ConnectionPool = ConnectionPool{
			MaxIdleTime: ptr(30 * time.Second),
			MaxIdle:     ptr(5),
		}

		require.Equal(t, expected, cfg)
	})

	cfg = factory.CreateDefaultConfig()

	t.Run("postgresql/all", func(t *testing.T) {
		sub, err := cm.Sub(component.NewIDWithName(metadata.Type, "all").String())
		require.NoError(t, err)
		require.NoError(t, sub.Unmarshal(cfg))

		expected := factory.CreateDefaultConfig().(*Config)
		expected.Endpoint = "localhost:5432"
		expected.Transport = confignet.TransportTypeTCP
		expected.Username = "otel"
		expected.Password = "${env:POSTGRESQL_PASSWORD}"
		expected.Databases = []string{"otel"}
		expected.ExcludeDatabases = []string{"template0"}
		expected.ControllerConfig.CollectionInterval = 10 * time.Second
		expected.ClientConfig = configtls.ClientConfig{
			Insecure:           false,
			InsecureSkipVerify: false,
			Config: configtls.Config{
				CAFile:   "/home/otel/authorities.crt",
				CertFile: "/home/otel/mypostgrescert.crt",
				KeyFile:  "/home/otel/mypostgreskey.key",
			},
		}
		expected.ConnectionPool = ConnectionPool{
			MaxIdleTime: ptr(30 * time.Second),
			MaxLifetime: ptr(time.Minute),
			MaxIdle:     ptr(5),
			MaxOpen:     ptr(10),
		}

		require.Equal(t, expected, cfg)
	})
}

func ptr[T any](value T) *T {
	return &value
}
