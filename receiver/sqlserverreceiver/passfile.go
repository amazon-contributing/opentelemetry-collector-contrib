// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sqlserverreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver"

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// resolvePassfileEntry reads an ADO-style passfile and returns the full connection
// string line matching the given server, port, and username.
//
// The passfile contains one ADO connection string per line (one per SQL Server instance):
//
//	server=localhost;user id=cw_monitor;password=CwMon@1234567;port=1433
//	server=127.0.0.1;user id=cw_monitor;password=SecondPass@456;port=1434
//
// Each line is a standard ADO connection string (semicolon-separated key=value pairs).
// The function matches lines by comparing server, port, and user id values.
// The first matching line is returned as-is — it can be passed directly to sql.Open().
//
// Lines starting with # are treated as comments. Empty lines are skipped.
func resolvePassfileEntry(path string, server string, port uint, username string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("unable to open passfile %q: %w", path, err)
	}
	defer file.Close()

	portStr := strconv.FormatUint(uint64(port), 10)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		params := parseADOConnectionString(line)

		// Match by server, port, and user id
		lineServer := params["server"]
		linePort := params["port"]
		lineUser := params["user id"]

		// Server in ADO format may include port as "server,port"
		if strings.Contains(lineServer, ",") {
			parts := strings.SplitN(lineServer, ",", 2)
			lineServer = strings.TrimSpace(parts[0])
			if linePort == "" {
				linePort = strings.TrimSpace(parts[1])
			}
		}

		if strings.EqualFold(lineServer, server) &&
			linePort == portStr &&
			strings.EqualFold(lineUser, username) {
			return line, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading passfile %q: %w", path, err)
	}

	return "", fmt.Errorf("no matching entry found in passfile %q for server=%q port=%d username=%q", path, server, port, username)
}

// parseADOConnectionString parses an ADO connection string into a map of
// normalized key-value pairs. Keys are lowercased and ADO synonyms are
// resolved to canonical names.
func parseADOConnectionString(connStr string) map[string]string {
	result := make(map[string]string)

	parts := splitADOParts(connStr)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eqIdx := strings.IndexByte(part, '=')
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eqIdx])
		value := strings.TrimSpace(part[eqIdx+1:])

		// Remove surrounding double quotes if present
		if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = value[1 : len(value)-1]
			// Unescape doubled double quotes
			value = strings.ReplaceAll(value, "\"\"", "\"")
		}

		canonicalKey := normalizeADOKey(key)
		result[canonicalKey] = value
	}

	return result
}

// splitADOParts splits an ADO connection string by semicolons, respecting
// double-quoted values that may contain semicolons.
func splitADOParts(connStr string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false

	for i := 0; i < len(connStr); i++ {
		ch := connStr[i]
		switch {
		case ch == '"':
			if inQuotes && i+1 < len(connStr) && connStr[i+1] == '"' {
				// Escaped quote inside quoted value
				current.WriteByte(ch)
				current.WriteByte(connStr[i+1])
				i++
			} else {
				inQuotes = !inQuotes
				current.WriteByte(ch)
			}
		case ch == ';' && !inQuotes:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(ch)
		}
	}
	// Append remaining content
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// normalizeADOKey maps ADO connection string key synonyms to canonical names.
func normalizeADOKey(key string) string {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch lower {
	case "password", "pwd":
		return "password"
	case "server", "data source", "address", "addr", "network address":
		return "server"
	case "user id", "user", "uid":
		return "user id"
	case "port":
		return "port"
	case "database", "initial catalog":
		return "database"
	default:
		return lower
	}
}
