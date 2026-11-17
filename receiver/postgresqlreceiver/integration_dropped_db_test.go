// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package postgresqlreceiver

import (
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strings"
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

// Each statement reads a different catalog, so each gets its own queryid and the
// queryid-keyed delta cache cannot mix them up. No catalog name is a prefix of
// another one used here.
const (
	liveDatabaseCatalog    = "pg_namespace"
	liveDatabaseCatalog2   = "pg_tablespace"
	liveDatabaseLater      = "pg_cast" // run between the two scrapes
	droppedRoleCatalog     = "pg_aggregate"
	droppedDBCatalog       = "pg_language"
	oldIncarnationCatalog  = "pg_collation"
	newIncarnationCatalog  = "pg_opclass"
	newIncarnationCatalog2 = "pg_opfamily"
	newIncarnationLater    = "pg_conversion" // run between the two scrapes
)

// countFrom is the statement each scenario runs against its catalog.
func countFrom(catalog string) string { return "SELECT count(*) FROM " + catalog }

// TestIntegrationTopQueryOrphanRows checks, against a live PostgreSQL 13 and 17,
// top query collection when pg_stat_statements holds rows whose database or role
// no longer exists:
//   - a dropped database: its rows no longer match pg_database;
//   - a role dropped while its database lives on: its rows no longer match pg_roles,
//     so their rolname is NULL and this fork's `rolname != current_user` leaves
//     them out (the COALESCE of rolname to an empty string in the select list does
//     not bring them back). Upstream v0.165.1, which the fork's sync (#710) brings
//     in, filters on the COALESCE instead and reports these rows with an empty
//     rolname, so that sync has to flip this expectation;
//   - a database dropped and recreated under the same name: the old rows keep the
//     old dbid, which no longer joins pg_database; only the new incarnation's rows
//     do.
//
// The top query SQL must return none of those rows. Two scrapes must not panic.
// The first must report the live databases and the new incarnation. The second
// runs against the counters the first one cached, so it must report only the
// statements run between the scrapes.
//
// Not covered: a collector that already ran before a database is dropped and
// recreated can under-report the new incarnation's statements whose queryid is
// unchanged, until their counters pass the old ones. That comes from the delta
// cache being keyed by queryid alone, which predates this fix.
//
// CI picks this test up through the "TestIntegration" entry of the -run pattern
// that Makefile.Common builds from the test names in integration_test.go, so
// keep that name prefix. Keep this file name too: with a second file matching
// *integration_test.go, the unquoted find glob in Makefile.Common fails and no
// integration test would run.
func TestIntegrationTopQueryOrphanRows(t *testing.T) {
	for _, pgVersion := range []string{pre17TestVersion, post17TestVersion} {
		t.Run("postgres_"+pgVersion, func(t *testing.T) {
			runTopQueryOrphanRows(t, pgVersion)
		})
	}
}

func runTopQueryOrphanRows(t *testing.T, pgVersion string) {
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

	// Superuser connection to the maintenance database. CREATE/DROP DATABASE and
	// DROP ROLE run here, never connected to the target database.
	root := openPGConn(t, "root", "otel", "postgres", port)

	// A live database used by appuser (created by 01-init.sql) and by a role that is
	// dropped afterwards, while the database lives on.
	execPG(t, root, "CREATE DATABASE keepdb")
	execPG(t, root, "CREATE ROLE gone_role LOGIN PASSWORD 'gonepass'")
	runPG(t, "appuser", "apppass", "keepdb", port, countFrom(liveDatabaseCatalog), countFrom(liveDatabaseCatalog2))
	runPG(t, "gone_role", "gonepass", "keepdb", port, countFrom(droppedRoleCatalog))
	execPG(t, root, "DROP ROLE gone_role")

	// A dropped database.
	execPG(t, root, "CREATE DATABASE dropme")
	runPG(t, "appuser", "apppass", "dropme", port, countFrom(droppedDBCatalog))
	execPG(t, root, "DROP DATABASE dropme")

	// A database dropped and recreated under the same name.
	execPG(t, root, "CREATE DATABASE recreated")
	runPG(t, "appuser", "apppass", "recreated", port, countFrom(oldIncarnationCatalog))
	execPG(t, root, "DROP DATABASE recreated")
	execPG(t, root, "CREATE DATABASE recreated")
	runPG(t, "appuser", "apppass", "recreated", port, countFrom(newIncarnationCatalog), countFrom(newIncarnationCatalog2))

	// Each scenario must leave its own orphan row behind, otherwise its checks below
	// would pass without meeting one.
	for _, catalog := range []string{droppedDBCatalog, oldIncarnationCatalog} {
		require.Positive(t, countPG(t, root, fmt.Sprintf(
			"SELECT count(*) FROM pg_stat_statements s LEFT JOIN pg_database d ON s.dbid = d.oid WHERE d.oid IS NULL AND s.query = '%s'",
			countFrom(catalog))), "expected a pg_stat_statements row of a dropped database for %s", catalog)
	}
	require.Positive(t, countPG(t, root, fmt.Sprintf(
		"SELECT count(*) FROM pg_stat_statements s LEFT JOIN pg_roles r ON s.userid = r.oid WHERE r.oid IS NULL AND s.query = '%s'",
		countFrom(droppedRoleCatalog))), "expected a pg_stat_statements row of the dropped role")

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

	const (
		maxRows    = int64(5000)
		topN       = int64(5000)
		maxExplain = int64(5000)
		interval   = time.Minute
	)

	// The SQL on its own, read straight from the client: every row has a database
	// name and a role name, so no row of the dropped database, the dropped role or
	// the old incarnation comes back.
	sqlClient, err := clientFactory.getClient("postgres")
	require.NoError(t, err)
	sqlRows, err := sqlClient.getTopQuery(ctx, maxRows, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, sqlClient.Close())
	fromSQL := map[string][]string{}
	for _, row := range sqlRows {
		namespace, ok := row["db.namespace"].(string)
		require.True(t, ok, "the top query SQL returned a row without a database name")
		rolname, _ := row[dbAttributePrefix+"rolname"].(string)
		require.NotEmpty(t, rolname, "the top query SQL returned a row without a role name")
		query, _ := row["db.query.text"].(string)
		fromSQL[namespace] = append(fromSQL[namespace], query)
	}
	requireOrphanRowsLeftOut(t, fromSQL)
	requireQueryReported(t, fromSQL, "keepdb", liveDatabaseCatalog)
	requireQueryReported(t, fromSQL, "recreated", newIncarnationCatalog)

	// The delta cache gets room for the counters of every row the query can return
	// (10 per row, doubled as in the factory). A cache too small for the rows evicts
	// entries during the first scrape, and the second then reports those rows again
	// as if they were new.
	scraper := newPostgreSQLScraper(receiver.Settings{
		TelemetrySettings: component.TelemetrySettings{
			Logger: zap.Must(zap.NewProduction()),
		},
	}, &cfg, clientFactory, newCache(int(maxRows*10*2)), newTTLCache[string](1, time.Second))

	// scrape runs one top query collection and returns the reported queries by database.
	scrape := func() map[string][]string {
		var (
			logs      plog.Logs
			scrapeErr error
		)
		// A backstop: NotPanics turns a panic on an orphan row into a test failure
		// instead of aborting the whole test binary. The guard itself is covered by
		// TestScrapeTopQueriesSkipsNilDatabaseKeepsLiveRows.
		require.NotPanics(t, func() {
			logs, scrapeErr = scraper.scrapeTopQuery(ctx, maxRows, topN, maxExplain, interval)
		})
		require.NoError(t, scrapeErr)
		reported := topQueriesByDatabase(t, logs)
		requireOrphanRowsLeftOut(t, reported)
		return reported
	}

	// Cold caches: every live row is reported, including the new incarnation's.
	first := scrape()
	requireQueryReported(t, first, "keepdb", liveDatabaseCatalog)
	requireQueryReported(t, first, "recreated", newIncarnationCatalog)

	// Between the scrapes only these two statements run, apart from the collector's
	// own queries, which the SQL leaves out. A row whose counters did not move has
	// no delta, so the second scrape must report these two and nothing else: none
	// of the earlier statements may be reported again.
	runPG(t, "appuser", "apppass", "keepdb", port, countFrom(liveDatabaseLater))
	runPG(t, "appuser", "apppass", "recreated", port, countFrom(newIncarnationLater))
	// Rewind so the interval gate permits a second collection.
	scraper.lastExecutionTimestamp = scraper.lastExecutionTimestamp.Add(-interval)
	second := scrape()
	require.Len(t, second, 2, "the second scrape must report only the statements run since the first one; reported: %v", second)
	for database, table := range map[string]string{
		"keepdb":    liveDatabaseLater,
		"recreated": newIncarnationLater,
	} {
		require.Len(t, second[database], 1, "the second scrape must report only the statement run since the first one in %q; reported: %v", database, second[database])
		requireQueryReported(t, second, database, table)
	}
}

// requireOrphanRowsLeftOut fails if any reported query belongs to the dropped
// database, the dropped role or the dropped incarnation of the recreated database.
func requireOrphanRowsLeftOut(t *testing.T, queriesByDatabase map[string][]string) {
	t.Helper()
	for database, queries := range queriesByDatabase {
		for _, query := range queries {
			require.NotContains(t, query, droppedDBCatalog, "a row of the dropped database was reported under %q", database)
			require.NotContains(t, query, droppedRoleCatalog, "a row of the dropped role was reported under %q", database)
			require.NotContains(t, query, oldIncarnationCatalog, "a row of the dropped incarnation was reported under %q", database)
		}
	}
}

// requireQueryReported fails unless database has a reported query that reads table.
func requireQueryReported(t *testing.T, queriesByDatabase map[string][]string, database, table string) {
	t.Helper()
	for _, query := range queriesByDatabase[database] {
		if strings.Contains(query, table) {
			return
		}
	}
	require.Failf(t, "query not reported", "no reported query reads %s in %q; reported: %v", table, database, queriesByDatabase[database])
}

// topQueriesByDatabase returns the query texts of the top query records in logs by
// database, and fails the test if a record carries no database name.
func topQueriesByDatabase(t *testing.T, logs plog.Logs) map[string][]string {
	t.Helper()
	queries := map[string][]string{}
	for i := range logs.ResourceLogs().Len() {
		scopeLogs := logs.ResourceLogs().At(i).ScopeLogs()
		for j := range scopeLogs.Len() {
			records := scopeLogs.At(j).LogRecords()
			for k := range records.Len() {
				attrs := records.At(k).Attributes()
				namespace, ok := attrs.Get("db.namespace")
				require.True(t, ok, "every top query record must carry a database name")
				require.NotEmpty(t, namespace.Str())
				query, _ := attrs.Get("db.query.text")
				queries[namespace.Str()] = append(queries[namespace.Str()], query.Str())
			}
		}
	}
	return queries
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

// runPG runs queries as user in dbname and closes the connection, so the session
// is gone before its database or role is dropped.
func runPG(t *testing.T, user, pass, dbname, port string, queries ...string) {
	t.Helper()
	db := openPGConn(t, user, pass, dbname, port)
	for _, q := range queries {
		rows, err := db.Query(q)
		require.NoError(t, err)
		// drain the result set so the statement is fully executed and recorded
		for rows.Next() {
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	require.NoError(t, db.Close())
}

func countPG(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(q).Scan(&n))
	return n
}
