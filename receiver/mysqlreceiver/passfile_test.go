// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mysqlreceiver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePasswordFromPassfile(t *testing.T) {
	content := `# comment line
localhost:3306:*:cw_monitor:secret123
db1.internal.example.com:3306:production:app_user:p@ssw0rd
*:*:*:wildcard_user:wildcard_pass
`
	dir := t.TempDir()
	path := filepath.Join(dir, ".mysql_credentials")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	tests := []struct {
		name     string
		host     string
		port     string
		database string
		username string
		wantPass string
		wantErr  bool
	}{
		{
			name:     "exact match",
			host:     "localhost",
			port:     "3306",
			database: "testdb",
			username: "cw_monitor",
			wantPass: "secret123",
		},
		{
			name:     "wildcard database match",
			host:     "localhost",
			port:     "3306",
			database: "anything",
			username: "cw_monitor",
			wantPass: "secret123",
		},
		{
			name:     "hostname wildcard match",
			host:     "db1.internal.example.com",
			port:     "3306",
			database: "production",
			username: "app_user",
			wantPass: "p@ssw0rd",
		},
		{
			name:     "full wildcard match",
			host:     "any-host",
			port:     "5555",
			database: "any-db",
			username: "wildcard_user",
			wantPass: "wildcard_pass",
		},
		{
			name:     "no match",
			host:     "unknown",
			port:     "3306",
			database: "testdb",
			username: "unknown_user",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pass, err := resolvePasswordFromPassfile(path, tt.host, tt.port, tt.database, tt.username)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantPass, pass)
			}
		})
	}
}

func TestResolvePasswordFromPassfile_EscapedColons(t *testing.T) {
	content := `localhost:3306:*:user\:name:pass\:word
`
	dir := t.TempDir()
	path := filepath.Join(dir, ".mysql_credentials")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	pass, err := resolvePasswordFromPassfile(path, "localhost", "3306", "db", "user:name")
	require.NoError(t, err)
	assert.Equal(t, "pass:word", pass)
}

func TestResolvePasswordFromPassfile_FileNotFound(t *testing.T) {
	_, err := resolvePasswordFromPassfile("/nonexistent/path", "localhost", "3306", "db", "user")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to open passfile")
}

func TestSplitPassfileLine(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"localhost:3306:db:user:pass", []string{"localhost", "3306", "db", "user", "pass"}},
		{`host:3306:db:user:pass\:with\:colons`, []string{"host", "3306", "db", "user", "pass:with:colons"}},
		{"*:*:*:*:secret", []string{"*", "*", "*", "*", "secret"}},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := splitPassfileLine(tt.line)
			assert.Equal(t, tt.want, got)
		})
	}
}
