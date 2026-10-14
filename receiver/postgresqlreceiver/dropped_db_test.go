// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver/internal/metadata"
)

// topQueryColsDropDB is the ordered column list returned by the top-query SQL
// (see templates/topQueryTemplate.tmpl). It must list every column the delta
// loop and the event-record loop read, otherwise the delta loop panics for the
// wrong reason before we ever reach the dropped-database code paths.
var topQueryColsDropDB = []string{
	"calls",
	"datname",
	"shared_blks_dirtied",
	"shared_blks_hit",
	"shared_blks_read",
	"shared_blks_written",
	"local_blks_hit",
	"local_blks_read",
	"local_blks_dirtied",
	"local_blks_written",
	"temp_blks_read",
	"temp_blks_written",
	"query",
	"queryid",
	"rolname",
	"rolsuper",
	"rows",
	"total_exec_time",
	"total_plan_time",
}

// addTopQueryRow appends one top-query row. Pass a literal nil for datname to
// model a dropped-database orphan row: the sqlquery layer omits NULL columns,
// so db.namespace ends up absent in the attribute map. A real value (e.g. nil)
// must NOT go through FromCSVString, which would stringify it to "<nil>".
// totalExecTime must be > 0 (in ms) so the row survives the delta filter.
func addTopQueryRow(rows *sqlmock.Rows, datname any, queryID, rolsuper, calls, totalExecTime string) {
	rows.AddRow(
		calls,   // calls
		datname, // datname (nil => SQL NULL => omitted by the sqlquery layer)
		"1111",  // shared_blks_dirtied
		"1112",  // shared_blks_hit
		"1113",  // shared_blks_read
		"1114",  // shared_blks_written
		"2001",  // local_blks_hit
		"2002",  // local_blks_read
		"2003",  // local_blks_dirtied
		"2004",  // local_blks_written
		"1115",  // temp_blks_read
		"1116",  // temp_blks_written
		"select * from pg_stat_activity where id = 32", // query
		queryID,       // queryid
		"appuser",     // rolname (constant: any role != the monitor's current_user)
		rolsuper,      // rolsuper
		"30",          // rows
		totalExecTime, // total_exec_time
		"12000",       // total_plan_time
	)
}

// recordingClientFactory wraps a sqlmock-backed postgreSQLClient and records
// every database name getClient is asked for. It lets a test assert that the
// scraper never requests a client for an empty ("") database name, which is
// what would happen if a dropped-database orphan row (nil datname) leaked into
// the EXPLAIN path.
type recordingClientFactory struct {
	db        *sql.DB
	mu        sync.Mutex
	requested []string
}

func (f *recordingClientFactory) getClient(database string) (client, error) {
	f.mu.Lock()
	f.requested = append(f.requested, database)
	f.mu.Unlock()
	return &postgreSQLClient{client: f.db, closeFn: func() error { return nil }}, nil
}

func (*recordingClientFactory) close() error { return nil }

func (f *recordingClientFactory) requestedDatabases() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requested))
	copy(out, f.requested)
	return out
}

func newTopQueryTestSettings(t *testing.T) receiver.Settings {
	t.Helper()
	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	require.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{Logger: logger}
	return settings
}

func newTopQueryDropDBConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerTopQuery.Enabled = true
	return cfg
}

// TestScrapeTopQueriesSkipsDroppedDatabaseRows feeds a single top-query row
// whose datname is NULL (a dropped-database orphan). On the UNPATCHED tree the
// unchecked db.namespace type assertion panics:
//   - non_superuser exercises scraper.go:364 (the EXPLAIN path).
//   - superuser     exercises scraper.go:386 (the event-record path; the
//     EXPLAIN block is skipped for superuser-owned queries).
//
// The panic text is "interface conversion: interface {} is nil, not string".
func TestScrapeTopQueriesSkipsDroppedDatabaseRows(t *testing.T) {
	cases := []struct {
		name     string
		rolsuper string
	}{
		{name: "non_superuser", rolsuper: "false"},
		{name: "superuser", rolsuper: "true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer db.Close()

			factory := mockSimpleClientFactory{db: db}
			settings := newTopQueryTestSettings(t)

			rows := sqlmock.NewRows(topQueryColsDropDB)
			addTopQueryRow(rows, nil, "114514", tc.rolsuper, "123", "11000")
			mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(rows)

			scraper := newPostgreSQLScraper(settings, newTopQueryDropDBConfig(), factory, newCache(30), newTTLCache[string](1, time.Second))

			// UNPATCHED: panics on the nil datname. PATCHED: the orphan row is
			// skipped and no log record is produced for it.
			logs, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
			require.NoError(t, err)
			require.Equal(t, 0, logs.LogRecordCount(),
				"a dropped-database orphan row must not be reported")
		})
	}
}

// TestScrapeTopQueriesDroppedDatabaseMixedRows mixes a live row (real datname)
// with an orphan row (nil datname, different queryid). The orphan carries the
// higher total_exec_time so it is popped first. UNPATCHED it panics at
// scraper.go:364; PATCHED the orphan is dropped and getClient is only ever
// asked for real database names, never "".
func TestScrapeTopQueriesDroppedDatabaseMixedRows(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()

	factory := &recordingClientFactory{db: db}
	settings := newTopQueryTestSettings(t)

	rows := sqlmock.NewRows(topQueryColsDropDB)
	addTopQueryRow(rows, nil, "orphan-queryid", "false", "999", "40000")
	addTopQueryRow(rows, "app", "live-queryid", "false", "123", "11000")
	mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(rows)

	scraper := newPostgreSQLScraper(settings, newTopQueryDropDBConfig(), factory, newCache(30), newTTLCache[string](1, time.Second))

	// UNPATCHED: popping the orphan (nil datname) panics at scraper.go:364.
	_, err = scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
	require.NoError(t, err)

	// PATCHED invariant: getClient is never asked for an empty database name.
	for _, name := range factory.requestedDatabases() {
		require.NotEmpty(t, name, "getClient must never be called with an empty database name")
	}
	require.Contains(t, factory.requestedDatabases(), defaultPostgreSQLDatabase)
}

// TestScrapeTopQueriesDroppedDatabaseQueryIDCollision places the orphan row
// (nil datname, higher calls) FIRST, sharing its queryid with a later live row.
// This guards against the fix letting an orphan poison the shared-queryid cache
// entry used by the live row. UNPATCHED it panics at scraper.go:364.
func TestScrapeTopQueriesDroppedDatabaseQueryIDCollision(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()

	factory := &recordingClientFactory{db: db}
	settings := newTopQueryTestSettings(t)

	const sharedQueryID = "shared-queryid"
	rows := sqlmock.NewRows(topQueryColsDropDB)
	addTopQueryRow(rows, nil, sharedQueryID, "false", "999", "40000")
	addTopQueryRow(rows, "app", sharedQueryID, "false", "123", "11000")
	mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(rows)

	scraper := newPostgreSQLScraper(settings, newTopQueryDropDBConfig(), factory, newCache(30), newTTLCache[string](1, time.Second))

	// UNPATCHED: the orphan is pushed and popped first, panicking at scraper.go:364.
	_, err = scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
	require.NoError(t, err)

	for _, name := range factory.requestedDatabases() {
		require.NotEmpty(t, name, "getClient must never be called with an empty database name")
	}
}
