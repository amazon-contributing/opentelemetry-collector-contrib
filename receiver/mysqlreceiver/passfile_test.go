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

func writePassfile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".mysql_password")
	err := os.WriteFile(path, []byte(content), 0600)
	require.NoError(t, err)
	return path
}

func TestResolvePasswordFromPassfile(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		host     string
		port     string
		user     string
		wantPass string
		wantErr  bool
	}{
		{
			name:     "exact match by host+port+user",
			content:  "[client]\nhost=localhost\nport=3306\nuser=cw_monitor\npassword=pass_primary\n",
			host:     "localhost",
			port:     "3306",
			user:     "cw_monitor",
			wantPass: "pass_primary",
		},
		{
			name:     "multi-instance matches correct section",
			content:  "[primary]\nhost=localhost\nport=3306\nuser=cw_monitor\npassword=pass_primary\n\n[secondary]\nhost=127.0.0.1\nport=3307\nuser=cw_monitor\npassword=pass_secondary\n",
			host:     "127.0.0.1",
			port:     "3307",
			user:     "cw_monitor",
			wantPass: "pass_secondary",
		},
		{
			name:     "spaces around equals",
			content:  "[client]\nhost = localhost\nport = 3306\nuser = cw_monitor\npassword = spaced_pass\n",
			host:     "localhost",
			port:     "3306",
			user:     "cw_monitor",
			wantPass: "spaced_pass",
		},
		{
			name:     "double quoted password",
			content:  "[client]\nhost=localhost\nport=3306\nuser=cw_monitor\npassword=\"quoted_pass\"\n",
			host:     "localhost",
			port:     "3306",
			user:     "cw_monitor",
			wantPass: "quoted_pass",
		},
		{
			name:     "password with special characters",
			content:  "[client]\nhost=localhost\nport=3306\nuser=cw_monitor\npassword=P@ss:w0rd!#$\n",
			host:     "localhost",
			port:     "3306",
			user:     "cw_monitor",
			wantPass: "P@ss:w0rd!#$",
		},
		{
			name:     "password with equals sign",
			content:  "[client]\nhost=localhost\nport=3306\nuser=cw_monitor\npassword=abc=def=ghi\n",
			host:     "localhost",
			port:     "3306",
			user:     "cw_monitor",
			wantPass: "abc=def=ghi",
		},
		{
			name:     "case insensitive matching",
			content:  "[Client]\nHost=LOCALHOST\nPort=3306\nUser=CW_MONITOR\nPassword=case_test\n",
			host:     "localhost",
			port:     "3306",
			user:     "cw_monitor",
			wantPass: "case_test",
		},
		{
			name:    "no match - wrong user",
			content: "[client]\nhost=localhost\nport=3306\nuser=admin\npassword=admin_pass\n",
			host:    "localhost",
			port:    "3306",
			user:    "cw_monitor",
			wantErr: true,
		},
		{
			name:    "no match - wrong host",
			content: "[client]\nhost=remotehost\nport=3306\nuser=cw_monitor\npassword=pass\n",
			host:    "localhost",
			port:    "3306",
			user:    "cw_monitor",
			wantErr: true,
		},
		{
			name:    "no match - wrong port",
			content: "[client]\nhost=localhost\nport=3307\nuser=cw_monitor\npassword=pass\n",
			host:    "localhost",
			port:    "3306",
			user:    "cw_monitor",
			wantErr: true,
		},
		{
			name:    "no match - missing host field",
			content: "[client]\nport=3306\nuser=cw_monitor\npassword=pass\n",
			host:    "localhost",
			port:    "3306",
			user:    "cw_monitor",
			wantErr: true,
		},
		{
			name:    "no match - missing port field",
			content: "[client]\nhost=localhost\nuser=cw_monitor\npassword=pass\n",
			host:    "localhost",
			port:    "3306",
			user:    "cw_monitor",
			wantErr: true,
		},
		{
			name:    "no match - missing user field",
			content: "[client]\nhost=localhost\nport=3306\npassword=pass\n",
			host:    "localhost",
			port:    "3306",
			user:    "cw_monitor",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePassfile(t, tt.content)
			pass, err := resolvePasswordFromPassfile(path, tt.host, tt.port, "*", tt.user)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPass, pass)
		})
	}
}

func TestResolvePasswordFromPassfile_FileNotFound(t *testing.T) {
	_, err := resolvePasswordFromPassfile("/nonexistent/path", "localhost", "3306", "*", "cw_monitor")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to open password file")
}

func TestResolvePasswordFromPassfile_CommentsIgnored(t *testing.T) {
	content := "# This is a comment\n[client]\n; another comment\nhost=localhost\nport=3306\nuser=cw_monitor\npassword=after_comments\n"
	path := writePassfile(t, content)

	pass, err := resolvePasswordFromPassfile(path, "localhost", "3306", "*", "cw_monitor")
	require.NoError(t, err)
	assert.Equal(t, "after_comments", pass)
}
