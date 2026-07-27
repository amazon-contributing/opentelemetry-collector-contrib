// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sqlserverreceiver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePassfileEntry(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		server      string
		port        uint
		username    string
		expected    string
		expectError bool
	}{
		{
			name:     "returns full line for direct use with sql.Open",
			content:  "server=localhost;user id=cw_monitor;password=CwMon@1234567;port=1433\n",
			server:   "localhost",
			port:     1433,
			username: "cw_monitor",
			expected: "server=localhost;user id=cw_monitor;password=CwMon@1234567;port=1433",
		},
		{
			name: "multi-instance returns correct full line",
			content: "server=localhost;user id=cw_monitor;password=First;port=1433\n" +
				"server=127.0.0.1;user id=cw_monitor;password=Second;port=1434\n",
			server:   "127.0.0.1",
			port:     1434,
			username: "cw_monitor",
			expected: "server=127.0.0.1;user id=cw_monitor;password=Second;port=1434",
		},
		{
			name:        "no match returns error",
			content:     "server=otherhost;user id=sa;password=X;port=1433\n",
			server:      "localhost",
			port:        1433,
			username:    "sa",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			passfile := filepath.Join(dir, ".sqlserver_password")
			err := os.WriteFile(passfile, []byte(tc.content), 0o600)
			require.NoError(t, err)

			result, err := resolvePassfileEntry(passfile, tc.server, tc.port, tc.username)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestResolvePassfileEntry_FileNotFound(t *testing.T) {
	_, err := resolvePassfileEntry("/nonexistent/path/passfile", "server", 1433, "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to open passfile")
}

func TestValidatePassfilePermissions(t *testing.T) {
	dir := t.TempDir()

	t.Run("file does not exist", func(t *testing.T) {
		cfg := &Config{Passfile: filepath.Join(dir, "nonexistent")}
		err := cfg.validatePassfilePermissions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inaccessible")
	})

	t.Run("file with 0600 permissions", func(t *testing.T) {
		path := filepath.Join(dir, "pass_0600")
		err := os.WriteFile(path, []byte("content"), 0o600)
		require.NoError(t, err)

		cfg := &Config{Passfile: path}
		err = cfg.validatePassfilePermissions()
		require.NoError(t, err)
	})

	t.Run("file with 0400 permissions", func(t *testing.T) {
		path := filepath.Join(dir, "pass_0400")
		err := os.WriteFile(path, []byte("content"), 0o400)
		require.NoError(t, err)

		cfg := &Config{Passfile: path}
		err = cfg.validatePassfilePermissions()
		require.NoError(t, err)
	})

	if runtime.GOOS == "linux" {
		t.Run("file with 0644 permissions rejected on linux", func(t *testing.T) {
			path := filepath.Join(dir, "pass_0644")
			err := os.WriteFile(path, []byte("content"), 0o644) //nolint:gosec // intentionally lax permissions to exercise validation
			require.NoError(t, err)

			cfg := &Config{Passfile: path}
			err = cfg.validatePassfilePermissions()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "permissions must be 0600 or 0400")
		})

		t.Run("file with 0777 permissions rejected on linux", func(t *testing.T) {
			path := filepath.Join(dir, "pass_0777")
			err := os.WriteFile(path, []byte("content"), 0o777) //nolint:gosec // intentionally lax permissions to exercise validation
			require.NoError(t, err)

			cfg := &Config{Passfile: path}
			err = cfg.validatePassfilePermissions()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "permissions must be 0600 or 0400")
		})
	}
}

func TestParseADOConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		connStr  string
		expected map[string]string
	}{
		{
			name:    "standard connection string",
			connStr: "server=localhost;user id=cw_monitor;password=CwMon@1234567;port=1433",
			expected: map[string]string{
				"server":   "localhost",
				"user id":  "cw_monitor",
				"password": "CwMon@1234567",
				"port":     "1433",
			},
		},
		{
			name:    "server with comma-port",
			connStr: "server=localhost,1433;user id=sa;password=MyPass",
			expected: map[string]string{
				"server":   "localhost,1433",
				"user id":  "sa",
				"password": "MyPass",
			},
		},
		{
			name:    "quoted password with semicolons",
			connStr: "server=host;password=\"P@ss;w0rd\";port=1433",
			expected: map[string]string{
				"server":   "host",
				"password": "P@ss;w0rd",
				"port":     "1433",
			},
		},
		{
			name:    "quoted password with escaped quotes",
			connStr: "server=host;password=\"has\"\"quotes\"\"\";port=1433",
			expected: map[string]string{
				"server":   "host",
				"password": "has\"quotes\"",
				"port":     "1433",
			},
		},
		{
			name:    "synonyms normalized",
			connStr: "Data Source=host;uid=admin;pwd=secret;Initial Catalog=mydb",
			expected: map[string]string{
				"server":   "host",
				"user id":  "admin",
				"password": "secret",
				"database": "mydb",
			},
		},
		{
			name:    "password from design doc example",
			connStr: "server=localhost;user id=cw_monitor;password=\"P@ss;w0rd#\"\"123}\";port=1433",
			expected: map[string]string{
				"server":   "localhost",
				"user id":  "cw_monitor",
				"password": "P@ss;w0rd#\"123}",
				"port":     "1433",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseADOConnectionString(tc.connStr)
			for key, expectedVal := range tc.expected {
				assert.Equal(t, expectedVal, result[key], "key %q mismatch", key)
			}
		})
	}
}
