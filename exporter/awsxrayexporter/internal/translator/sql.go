// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package translator // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/translator"

import (
	"cmp"
	"net"
	"net/url"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	conventions "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/metadata"
	awsxray "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/xray"
)

func makeSQL(span ptrace.Span, attributes map[string]pcommon.Value) (map[string]pcommon.Value, *awsxray.SQLData) {
	var (
		filtered           = make(map[string]pcommon.Value)
		sqlData            awsxray.SQLData
		dbURL              string
		dbConnectionString string
		dbSystem           string
		dbInstance         string
		dbStatement        string
		dbUser             string
		// Legacy v1.12.0 values are kept apart so the new keys win regardless of map iteration order.
		legacySystem    string
		legacyInstance  string
		legacyStatement string
		legacyUser      string
	)

	for key, value := range attributes {
		switch key {
		// New v1.43.0 keys — gate-guarded by EmitV1DB:
		case string(conventions.DBNamespaceKey):
			if metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate.IsEnabled() {
				dbInstance = value.Str()
			} else {
				filtered[key] = value
			}
		case string(conventions.DBSystemNameKey):
			if metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate.IsEnabled() {
				dbSystem = value.Str()
			} else {
				filtered[key] = value
			}
		case string(conventions.DBQueryTextKey):
			if metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate.IsEnabled() {
				dbStatement = value.Str()
			} else {
				filtered[key] = value
			}
		case string(conventions.UserNameKey):
			if metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate.IsEnabled() {
				dbUser = value.Str()
			} else {
				filtered[key] = value
			}
		// Old v1.12.0 keys — gate-guarded:
		// TODO: Remove these cases when exporter.awsxray.DontEmitV0DBConventions is removed.
		case "db.connection_string":
			if !metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate.IsEnabled() {
				dbConnectionString = value.Str()
			} else {
				filtered[key] = value
			}
		case "db.name":
			if !metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate.IsEnabled() {
				legacyInstance = value.Str()
			} else {
				filtered[key] = value
			}
		case "db.statement":
			if !metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate.IsEnabled() {
				legacyStatement = value.Str()
			} else {
				filtered[key] = value
			}
		case "db.system":
			if !metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate.IsEnabled() {
				legacySystem = value.Str()
			} else {
				filtered[key] = value
			}
		case "db.user":
			if !metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate.IsEnabled() {
				legacyUser = value.Str()
			} else {
				filtered[key] = value
			}
		default:
			filtered[key] = value
		}
	}

	// X-Ray has always shown the legacy names for systems that current conventions renamed, so keep
	// them: old and new SDK data (and segments re-exported from the X-Ray receiver) stay identical.
	dbSystem = cmp.Or(legacyDBSystemName(dbSystem), legacySystem)
	if dbInstance != "" {
		dbInstance = databaseName(dbInstance, dbSystem)
	}
	dbInstance = cmp.Or(dbInstance, legacyInstance)
	dbStatement = cmp.Or(dbStatement, legacyStatement)
	dbUser = cmp.Or(dbUser, legacyUser)

	if !isSQL(dbSystem) {
		// Either no DB attributes or this is not an SQL DB.
		return attributes, nil
	}

	// Despite what the X-Ray documents say, having the DB connection string
	// set as the URL value of the segment is not useful. So let's use the
	// current span name instead
	dbURL = span.Name()

	// Let's keep the original format for connection_string
	// db.connection_string has no replacement in current conventions, so keep it whenever the SDK
	// still sends it. Otherwise, with EmitV1DB, rebuild it from the endpoint in the same
	// "<system>://<host>:<port>" shape that instrumentations used for db.connection_string, so
	// old and new SDK data produce the same X-Ray connection string.
	if dbConnectionString == "" && metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate.IsEnabled() {
		if host, port := databaseEndpoint(span.Attributes()); host != "" {
			port = cmp.Or(port, defaultDatabasePorts[dbSystem])
			dbConnectionString = dbSystem + "://" + host
			if port != "" {
				dbConnectionString = dbSystem + "://" + net.JoinHostPort(host, port)
			} else if strings.Contains(host, ":") {
				dbConnectionString = dbSystem + "://[" + host + "]"
			}
		}
	}
	if dbConnectionString == "" {
		dbConnectionString = "localhost"
	}
	dbConnectionString += "/" + dbInstance

	sqlData = awsxray.SQLData{
		URL:              awsxray.String(dbURL),
		ConnectionString: awsxray.String(dbConnectionString),
		DatabaseType:     awsxray.String(dbSystem),
		User:             awsxray.String(dbUser),
		SanitizedQuery:   awsxray.String(dbStatement),
	}
	return filtered, &sqlData
}

// databaseHost returns the database host used in "<db name>@<db host>" segment names when
// EmitV1DB is enabled: server.address, then the legacy db.connection_string, then net.peer.name.
func databaseHost(attributes pcommon.Map) string {
	if host, ok := attributes.Get(string(conventions.ServerAddressKey)); ok && host.Str() != "" {
		return host.Str()
	}
	if !metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate.IsEnabled() {
		if connection, ok := attributes.Get("db.connection_string"); ok {
			if parsed, _ := url.Parse(strings.TrimPrefix(connection.Str(), "jdbc:")); parsed != nil && parsed.Hostname() != "" {
				return parsed.Hostname()
			}
		}
	}
	host, _ := databaseEndpoint(attributes)
	return host
}

// databaseEndpoint returns the database host and port from server.address/server.port, falling
// back to the legacy net.peer.name/net.peer.port unless DontEmitV0HTTPNetworkConventions is enabled.
func databaseEndpoint(attributes pcommon.Map) (host, port string) {
	legacy := !metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled()
	hostKey, portKey := string(conventions.ServerAddressKey), string(conventions.ServerPortKey)
	value, ok := attributes.Get(hostKey)
	if (!ok || value.Str() == "") && legacy {
		hostKey, portKey = "net.peer.name", "net.peer.port"
		value, ok = attributes.Get(hostKey)
	}
	if !ok || value.Str() == "" {
		return "", ""
	}
	if p, ok := attributes.Get(portKey); ok {
		port = p.Str()
		if port == "" {
			port = strconv.FormatInt(p.Int(), 10)
		}
	}
	return value.Str(), port
}

// databaseName returns the database name from a db.namespace value. Current conventions
// qualify the namespace for some systems ("<database>|<schema>" for PostgreSQL,
// "<instance>|<database>" for SQL Server); X-Ray only needs the database name, and "|" is not a
// valid character in X-Ray segment names.
func databaseName(namespace, system string) string {
	first, last, qualified := strings.Cut(namespace, "|")
	if !qualified {
		return namespace
	}
	if system == conventions.DBSystemNameMicrosoftSQLServer.Value.AsString() || system == "mssql" {
		return last
	}
	return first
}

// defaultDatabasePorts lists well-known default ports. Current conventions omit server.port when
// it is the default, while legacy db.connection_string values always carried it.
var defaultDatabasePorts = map[string]string{
	"postgresql": "5432",
	"mysql":      "3306",
	"mariadb":    "3306",
	"mssql":      "1433",
	conventions.DBSystemNameMicrosoftSQLServer.Value.AsString(): "1433",
	"oracle": "1521",
	conventions.DBSystemNameOracleDB.Value.AsString(): "1521",
	"db2": "50000",
	conventions.DBSystemNameIBMDB2.Value.AsString(): "50000",
}

func isSQL(system string) bool {
	switch system {
	// v1.12.0 values (default):
	// TODO: Remove v1.12.0 system name values when exporter.awsxray.DontEmitV0DBConventions is removed.
	case "db2", "mssql", "oracle":
		return true
	// Unchanged values (present in both v1.12.0 and v1.43.0):
	case "derby",
		"hive",
		"mariadb",
		"mysql",
		"other_sql",
		"postgresql",
		"sqlite",
		"teradata":
		return true
	// v1.43.0 values (only when EmitV1DB is enabled):
	// TODO: Remove this gate when exporter.awsxray.DontEmitV0DBConventions is removed.
	case conventions.DBSystemNameIBMDB2.Value.AsString(),
		conventions.DBSystemNameMicrosoftSQLServer.Value.AsString(),
		conventions.DBSystemNameOracleDB.Value.AsString():
		return metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate.IsEnabled()
	default:
	}
	return false
}

// legacyDBSystemName maps db.system.name values that were renamed in current conventions back to
// the db.system values used in X-Ray segments.
func legacyDBSystemName(system string) string {
	switch system {
	case conventions.DBSystemNameMicrosoftSQLServer.Value.AsString():
		return "mssql"
	case conventions.DBSystemNameOracleDB.Value.AsString():
		return "oracle"
	case conventions.DBSystemNameIBMDB2.Value.AsString():
		return "db2"
	default:
		return system
	}
}
