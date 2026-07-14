// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mysqlreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mysqlreceiver"

import (
	"fmt"
	"strings"

	"gopkg.in/ini.v1"
)

// resolvePasswordFromPassfile reads a MySQL option file (.my.cnf format) and
// resolves the password for the given connection parameters.
//
// Lookup strategy: Iterate all sections, match by host+port+user fields.
//
// File format (standard MySQL INI):
//
//	[client]
//	host=localhost
//	port=3306
//	user=cw_monitor
//	password=secret
func resolvePasswordFromPassfile(path, hostname, port, username string) (string, error) {
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
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, path)
	if err != nil {
		return nil, fmt.Errorf("unable to open password file: %w", err)
	}

	var sections []myCnfSection
	for _, s := range cfg.Sections() {
		if s.Name() == ini.DefaultSection {
			continue
		}
		fields := make(map[string]string)
		for _, k := range s.Keys() {
			fields[strings.ToLower(k.Name())] = k.Value()
		}
		sections = append(sections, myCnfSection{name: s.Name(), fields: fields})
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
