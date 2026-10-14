//go:build integration

// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

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
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/scraper/scraperhelper"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver/internal/metadata"
)

// TestIntegrationTopQueryDroppedDatabase reproduces the real-world crash end to
// end against a live PostgreSQL container (both 13 and 17): after DROP DATABASE,
// pg_stat_statements retains rows whose datname is now NULL, and scraping top
// queries over them panics on the unchecked db.namespace type assertion.
//
// It is written as a non-panicking test (require.NotPanics). On the UNPATCHED
// tree the two scrapeTopQuery calls panic, so NotPanics fails and reports the
// recovered "interface conversion: interface {} is nil, not string" panic. On
// the PATCHED tree the orphan rows are skipped and the test passes.
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
			// NOTE: the sibling TestScrapeLogsFromContainer pins ProviderPodman,
			// but this host has no podman binary; leaving ProviderType at its
			// zero value selects the Docker provider that is actually running.
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
	require.NoError(t, err)

	require.NoError(t, ci.Start(ctx))
	defer testcontainers.CleanupContainer(t, ci)

	mapped, err := ci.MappedPort(ctx, postgresqlPort)
	require.NoError(t, err)
	port := mapped.Port()

	// Superuser connection to the maintenance database. CREATE/DROP DATABASE
	// cannot run while connected to the target database, so keep this on postgres.
	rootPostgres := openPGConn(t, "root", "otel", "postgres", port)
	defer rootPostgres.Close()

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

	// The last two statements, also against a second (surviving) database, so
	// the same normalized queries exist under both a live and a doomed dbid.
	appKeep := openPGConn(t, "appuser", "apppass", "keepdb", port)
	queryPG(t, appKeep, "SELECT pg_sleep(0.2)")
	queryPG(t, appKeep, "SELECT 1")
	require.NoError(t, appKeep.Close())

	// Drop the database (FORCE terminates any lingering backends). Its
	// pg_stat_statements rows survive with a now-dangling dbid.
	execPG(t, rootPostgres, "DROP DATABASE dropme WITH (FORCE)")

	// Confirm orphan rows actually exist, otherwise the panic below would be a
	// false negative rather than a reproduction.
	var orphanCount int
	require.NoError(t, rootPostgres.QueryRow(
		"SELECT count(*) FROM pg_stat_statements s "+
			"LEFT JOIN pg_database d ON s.dbid = d.oid WHERE d.oid IS NULL").
		Scan(&orphanCount))
	require.Positive(t, orphanCount, "expected orphan pg_stat_statements rows after DROP DATABASE")

	// Fresh scraper (cold caches, tiny cache) connecting as the monitor user to
	// the postgres database, with generous topN / maxRows so every orphan row is
	// processed.
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

	// UNPATCHED: the first scrape panics on the orphan rows' nil datname; the
	// recover inside NotPanics turns that into a test failure that names the
	// nil-interface-conversion panic. PATCHED: both scrapes succeed.
	require.NotPanics(t, func() {
		_, _ = scraper.scrapeTopQuery(ctx, maxRows, topN, maxExplain, interval)
		// Rewind so the interval gate permits a second collection.
		scraper.lastExecutionTimestamp = scraper.lastExecutionTimestamp.Add(-interval)
		_, _ = scraper.scrapeTopQuery(ctx, maxRows, topN, maxExplain, interval)
	})
}

func openPGConn(t *testing.T, user, pass, dbname, port string) *sql.DB {
	t.Helper()
	connStr := fmt.Sprintf("postgres://%s:%s@localhost:%s/%s?sslmode=disable", user, pass, port, dbname)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
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
