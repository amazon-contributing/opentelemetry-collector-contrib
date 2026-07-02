// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mysqlreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mysqlreceiver"

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// resolvePasswordFromPassfile reads a MySQL option file (.my.cnf format) and
// resolves the password for the given connection parameters.
//
// Lookup strategy:
//  1. Iterate all sections, match by host+port+user fields.
//  2. If no section has match fields, fall back to the [client] section.
//
// File format (standard MySQL INI):
//
//	[client]
//	host=localhost
//	port=3306
//	user=cw_monitor
//	password=secret
func resolvePasswordFromPassfile(path, hostname, port, database, username string) (string, error) {
	sections, err := parseMyCnfFile(path)
	if err != nil {
		return "", err
	}
	return lookupByMatching(sections, hostname, port, username, path)
}

type myCnfSection struct {
	name   string
	fields map[string]string
}

func parseMyCnfFile(path string) ([]myCnfSection, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("unable to open password file: %w", err)
	}
	defer f.Close()

	var sections []myCnfSection
	var current *myCnfSection

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sectionName := strings.TrimSpace(line[1 : len(line)-1])
			sections = append(sections, myCnfSection{name: sectionName, fields: make(map[string]string)})
			current = &sections[len(sections)-1]
			continue
		}
		if current == nil {
			continue
		}
		key, value, found := parseINILine(line)
		if found {
			current.fields[strings.ToLower(key)] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading password file: %w", err)
	}
	return sections, nil
}

func lookupByMatching(sections []myCnfSection, hostname, port, username, path string) (string, error) {
	for _, s := range sections {
		sHost := s.fields["host"]
		sPort := s.fields["port"]
		sUser := s.fields["user"]
		if sHost == "" || sPort == "" || sUser == "" {
			continue
		}
		if matchField(sHost, hostname) && matchField(sPort, port) && matchField(sUser, username) {
			if pass, ok := s.fields["password"]; ok {
				return pass, nil
			}
		}
	}

	return "", fmt.Errorf("no matching entry found in password file %q for host=%s port=%s user=%s", path, hostname, port, username)
}

// matchField compares a field value against expected.
// Both fields must be present and match (case-insensitive).
func matchField(field, expected string) bool {
	if field == "" || expected == "" {
		return false
	}
	return strings.EqualFold(field, expected)
}

func parseINILine(line string) (string, string, bool) {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	value = stripINIQuotes(value)
	return key, value, true
}

func stripINIQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == 0x27 && s[len(s)-1] == 0x27) {
			return s[1 : len(s)-1]
		}
	}
	return s
}
