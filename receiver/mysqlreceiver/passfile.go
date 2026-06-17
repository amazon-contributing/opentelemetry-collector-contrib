// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mysqlreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mysqlreceiver"

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// resolvePasswordFromPassfile reads a pgpass-inspired credential file and resolves
// the password for the given connection parameters. The file format is:
//
//	hostname:port:database:username:password
//
// An asterisk (*) matches any value in that field.
func resolvePasswordFromPassfile(path, hostname, port, database, username string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("unable to open passfile: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := splitPassfileLine(line)
		if len(parts) != 5 {
			continue
		}

		if matchField(parts[0], hostname) &&
			matchField(parts[1], port) &&
			matchField(parts[2], database) &&
			matchField(parts[3], username) {
			return parts[4], nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading passfile: %w", err)
	}

	return "", fmt.Errorf("no matching entry found in passfile %q", path)
}

// splitPassfileLine splits a passfile line on unescaped colons.
// Backslash-escaped colons (\:) are treated as literal colons.
func splitPassfileLine(line string) []string {
	var parts []string
	var current strings.Builder

	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line):
			current.WriteByte(line[i+1])
			i++
		case line[i] == ':':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(line[i])
		}
	}
	parts = append(parts, current.String())
	return parts
}

func matchField(pattern, value string) bool {
	return pattern == "*" || pattern == value
}
