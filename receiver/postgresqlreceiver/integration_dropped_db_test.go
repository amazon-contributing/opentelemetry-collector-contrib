// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package postgresqlreceiver

import (
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/scraper/scraperhelper"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver/internal/metadata"
)

// TestIntegrationTopQueryDroppedDatabase checks, end to end against a live
// PostgreSQL container (both 13 and 17), that top query collection survives
// DROP DATABASE: pg_stat_statements keeps the dropped database's rows, but their
// dbid no longer matches pg_database, so the top query template's join yields a
// NULL datname for them. Scraping top queries must neither panic nor report a
// record for the dropped database, while the surviving database's rows are still
// reported. The scrape runs twice; the scraper's caches hold a single entry, so
// the second scrape cannot rely on warm caches.
//
// CI picks this test up through the "TestIntegration" entry of the -run pattern
// that Makefile.Common builds from the test names in integration_test.go, so
// keep that name prefix. Keep this file name too: with a second file matching
// *integration_test.go, the unquoted find glob in Makefile.Common fails and no
// integration test would run.
func TestIntegrationTopQueryDroppedDatabase(t *testing.T) {
	for _, pgVersion := range []string{pre17TestVersion, post17TestVersion} {
		t.Run("postgres_"+pgVersion, func(t *testing.T) {
			runTopQueryDroppedDatabase(t, pgVersion)
		})
	}
}

func runTopQueryDroppedDatabase(t *testing.T, pgVersion string) {
	ctx := t.Context()

	ci, err := testcontainers.GenericContainer(
		ctx,
		testcontainers.GenericContainerRequest{
			// ProviderType is left at its zero value (ProviderDefault), which picks
			// the container runtime from DOCKER_HOST and otherwise uses Docker.
			ContainerRequest: testcontainers.ContainerRequest{
				Image: fmt.Sprintf("postgres:%s", pgVersion),
				Env: map[string]string{
					"POSTGRES_USER":     "root",
					"POSTGRES_PASSWORD": "otel",
					"POSTGRES_DB":       "otel",
				},
				Files: []testcontainers.ContainerFile{
					{
						HostFilePath:      filepath.Join("testdata", "integration", "01-init.sql"),
						ContainerFilePath: "/docker-entrypoint-initdb.d/01-init.sql",
						FileMode:          700,
					},
					{
						HostFilePath:      filepath.Join("testdata", "integration", "02-create-extension.sh"),
						ContainerFilePath: "/docker-entrypoint-initdb.d/02-create-extension.sh",
						FileMode:          700,
					},
				},
				ExposedPorts: []string{postgresqlPort},
				Cmd: []string{
					"-c",
					"shared_preload_libraries=pg_stat_statements",
				},
				WaitingFor: wait.ForLog(".*port 5432").
					AsRegexp().
					WithOccurrence(1).
					WithStartupTimeout(2 * time.Minute),
			},
		})
	// Register the cleanup before any error check, as testcontainers documents, so
	// a container that was created but failed to start is still terminated.
	testcontainers.CleanupContainer(t, ci)
	require.NoError(t, err)

	require.NoError(t, ci.Start(ctx))

	mapped, err := ci.MappedPort(ctx, postgresqlPort)
	require.NoError(t, err)
	port := mapped.Port()

	// Superuser connection to the maintenance database. DROP DATABASE cannot run
	// while connected to the target database, so keep this on postgres.
	rootPostgres := openPGConn(t, "root", "otel", "postgres", port)

	execPG(t, rootPostgres, "CREATE DATABASE dropme")
	execPG(t, rootPostgres, "CREATE DATABASE keepdb")

	// In dropme: a table owned by the superuser, granted to the non-monitor
	// appuser role (created by 01-init.sql).
	rootDropme := openPGConn(t, "root", "otel", "dropme", port)
	execPG(t, rootDropme, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements")
	execPG(t, rootDropme, "CREATE TABLE orphan_t (id int)")
	execPG(t, rootDropme, "INSERT INTO orphan_t VALUES (1), (2), (3)")
	execPG(t, rootDropme, "GRANT USAGE ON SCHEMA public TO appuser")
	execPG(t, rootDropme, "GRANT SELECT ON orphan_t TO appuser")
	require.NoError(t, rootDropme.Close())

	// appuser (NOT a member of pg_monitor) runs statements against dropme so
	// pg_stat_statements records rows attributed to the dropme dbid.
	appDropme := openPGConn(t, "appuser", "apppass", "dropme", port)
	queryPG(t, appDropme, "SELECT * FROM orphan_t WHERE id = 1")
	queryPG(t, appDropme, "SELECT pg_sleep(0.2)")
	queryPG(t, appDropme, "SELECT 1")
	require.NoError(t, appDropme.Close())

	// Run the two table-independent statements against a second (surviving)
	// database, so the same normalized queries exist under both a live and a
	// doomed dbid.
	appKeep := openPGConn(t, "appuser", "apppass", "keepdb", port)
	queryPG(t, appKeep, "SELECT pg_sleep(0.2)")
	queryPG(t, appKeep, "SELECT 1")
	require.NoError(t, appKeep.Close())

	// Drop the database (FORCE terminates any lingering backends). Its
	// pg_stat_statements rows survive with a now-dangling dbid.
	execPG(t, rootPostgres, "DROP DATABASE dropme WITH (FORCE)")

	// Confirm orphan rows actually exist, otherwise the scrapes below would pass
	// without ever meeting one.
	var orphanCount int
	require.NoError(t, rootPostgres.QueryRow(
		"SELECT count(*) FROM pg_stat_statements s "+
			"LEFT JOIN pg_database d ON s.dbid = d.oid WHERE d.oid IS NULL").
		Scan(&orphanCount))
	require.Positive(t, orphanCount, "expected orphan pg_stat_statements rows after DROP DATABASE")

	// Fresh scraper (cold caches, tiny cache) connecting as the monitor user to
	// the postgres database, with generous topN / maxRows so that no row is cut
	// off by the limits.
	cfg := Config{
		Databases: []string{"postgres"},
		Username:  "otelu",
		Password:  "otelp",
		ControllerConfig: scraperhelper.ControllerConfig{
			CollectionInterval: time.Second,
		},
		ClientConfig: configtls.ClientConfig{Insecure: true},
		AddrConfig: confignet.AddrConfig{
			Endpoint: net.JoinHostPort("localhost", port),
		},
		LogsBuilderConfig: func() metadata.LogsBuilderConfig {
			lc := metadata.DefaultLogsBuilderConfig()
			lc.Events.DbServerTopQuery.Enabled = true
			return lc
		}(),
	}
	clientFactory := newDefaultClientFactory(&cfg)

	scraper := newPostgreSQLScraper(receiver.Settings{
		TelemetrySettings: component.TelemetrySettings{
			Logger: zap.Must(zap.NewProduction()),
		},
	}, &cfg, clientFactory, newCache(1), newTTLCache[string](1, time.Second))

	const (
		maxRows    = int64(5000)
		topN       = int64(5000)
		maxExplain = int64(5000)
		interval   = time.Minute
	)

	// scrape runs one top query collection and returns the databases it reported.
	scrape := func() map[string]struct{} {
		var (
			logs      plog.Logs
			scrapeErr error
		)
		// NotPanics turns a panic on an orphan row into a test failure instead
		// of aborting the whole test binary.
		require.NotPanics(t, func() {
			logs, scrapeErr = scraper.scrapeTopQuery(ctx, maxRows, topN, maxExplain, interval)
		})
		require.NoError(t, scrapeErr)
		databases := topQueryDatabases(t, logs)
		require.NotContains(t, databases, "dropme", "the dropped database must not be reported")
		return databases
	}

	// The first scrape still reports the surviving database's rows.
	require.Contains(t, scrape(), "keepdb")
	// Rewind so the interval gate permits a second collection.
	scraper.lastExecutionTimestamp = scraper.lastExecutionTimestamp.Add(-interval)
	_ = scrape()
}

// topQueryDatabases returns the names of the databases that the top query records
// in logs are attributed to, and fails the test if a record carries no database
// name.
func topQueryDatabases(t *testing.T, logs plog.Logs) map[string]struct{} {
	t.Helper()
	databases := map[string]struct{}{}
	for i := range logs.ResourceLogs().Len() {
		scopeLogs := logs.ResourceLogs().At(i).ScopeLogs()
		for j := range scopeLogs.Len() {
			records := scopeLogs.At(j).LogRecords()
			for k := range records.Len() {
				namespace, ok := records.At(k).Attributes().Get("db.namespace")
				require.True(t, ok, "every top query record must carry a database name")
				require.NotEmpty(t, namespace.Str())
				databases[namespace.Str()] = struct{}{}
			}
		}
	}
	return databases
}

// openPGConn opens a connection that is also closed when the test ends. Closing
// it earlier is fine: sql.DB.Close is idempotent.
func openPGConn(t *testing.T, user, pass, dbname, port string) *sql.DB {
	t.Helper()
	connStr := fmt.Sprintf("postgres://%s:%s@localhost:%s/%s?sslmode=disable", user, pass, port, dbname)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	return db
}

func execPG(t *testing.T, db *sql.DB, stmt string) {
	t.Helper()
	_, err := db.Exec(stmt)
	require.NoError(t, err)
}

func queryPG(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	rows, err := db.Query(q)
	require.NoError(t, err)
	// drain the result set so the statement is fully executed and recorded
	for rows.Next() {
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
}
