// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver/internal/metadata"
)

// topQueryTestColumns is the ordered column list returned by the top-query SQL
// (see templates/topQueryTemplate.tmpl). It must list every column the delta
// loop and the event-record loop read, otherwise the delta loop panics for the
// wrong reason before we ever reach the code under test.
var topQueryTestColumns = []string{
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

// topQueryText is the statement text carried by every row addTopQueryRow adds.
const topQueryText = "select * from pg_stat_activity where id = 32"

// addTopQueryRow appends one top-query row. Pass a literal nil for datname to
// model a dropped-database orphan row: the sqlquery layer omits NULL columns,
// so db.namespace ends up absent in the attribute map.
// totalExecTime must be > 0 (in ms) so the row survives the delta filter.
func addTopQueryRow(rows *sqlmock.Rows, datname any, queryID, rolsuper, calls, totalExecTime string) {
	rows.AddRow(
		calls,         // calls
		datname,       // datname (nil => SQL NULL => omitted by the sqlquery layer)
		"1111",        // shared_blks_dirtied
		"1112",        // shared_blks_hit
		"1113",        // shared_blks_read
		"1114",        // shared_blks_written
		"2001",        // local_blks_hit
		"2002",        // local_blks_read
		"2003",        // local_blks_dirtied
		"2004",        // local_blks_written
		"1115",        // temp_blks_read
		"1116",        // temp_blks_written
		topQueryText,  // query
		queryID,       // queryid
		"appuser",     // rolname (constant: any role != the monitor's current_user)
		rolsuper,      // rolsuper
		"30",          // rows
		totalExecTime, // total_exec_time
		"12000",       // total_plan_time
	)
}

func newTopQueryTestSettings(t *testing.T) receiver.Settings {
	t.Helper()
	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	require.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{Logger: logger}
	return settings
}

func newTopQueryTestConfig() *Config {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerTopQuery.Enabled = true
	return cfg
}

// TestScrapeTopQueriesSkipsDroppedDatabaseRows feeds a single top-query row
// whose datname is NULL (a dropped-database orphan) and expects it to be
// skipped, with neither a panic nor a log record. Both paths that read
// db.namespace are covered:
//   - non_superuser: the EXPLAIN path.
//   - superuser:     the event-record path (the EXPLAIN block is skipped for
//     superuser-owned queries).
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

			rows := sqlmock.NewRows(topQueryTestColumns)
			addTopQueryRow(rows, nil, "114514", tc.rolsuper, "123", "11000")
			mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(rows)

			scraper := newPostgreSQLScraper(settings, newTopQueryTestConfig(), factory, newCache(30), newTTLCache[string](1, time.Second))

			logs, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
			require.Equal(t, 0, logs.LogRecordCount(),
				"a dropped-database orphan row must not be reported")
		})
	}
}

// TestScrapeTopQueriesDroppedDatabaseMixedRows mixes an orphan row (nil datname)
// with a live row (real datname). The orphan comes first and carries the higher
// calls and total_exec_time, so it would be processed first if it were not
// skipped. Only the live row is reported. The live row is superuser-owned, so
// collectTopQuery skips EXPLAIN for it and the test does not depend on the
// EXPLAIN round trip. The cases are:
//   - distinct_queryids: the orphan has its own queryid.
//   - shared_queryid:    the orphan shares its queryid with the live row, so it
//     must be skipped before it can poison that queryid's delta-cache entries
//     (processed first, it would leave the live row with a zero delta).
func TestScrapeTopQueriesDroppedDatabaseMixedRows(t *testing.T) {
	cases := []struct {
		name          string
		orphanQueryID string
		liveQueryID   string
	}{
		{name: "distinct_queryids", orphanQueryID: "orphan-queryid", liveQueryID: "live-queryid"},
		{name: "shared_queryid", orphanQueryID: "shared-queryid", liveQueryID: "shared-queryid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			defer db.Close()

			factory := mockSimpleClientFactory{db: db}
			settings := newTopQueryTestSettings(t)

			rows := sqlmock.NewRows(topQueryTestColumns)
			addTopQueryRow(rows, nil, tc.orphanQueryID, "false", "999", "40000")
			addTopQueryRow(rows, "app", tc.liveQueryID, "true", "123", "11000")
			mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(rows)

			scraper := newPostgreSQLScraper(settings, newTopQueryTestConfig(), factory, newCache(30), newTTLCache[string](1, time.Second))

			logs, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
			requireOnlyTopQueryFor(t, logs, "app")
		})
	}
}

// requireOnlyTopQueryFor asserts that logs hold exactly one top query event and
// that it is attributed to database.
func requireOnlyTopQueryFor(t *testing.T, logs plog.Logs, database string) {
	t.Helper()
	// Without any record the builder emits no ResourceLogs, so check the count
	// first to fail with a message rather than an index-out-of-range panic.
	require.Equal(t, 1, logs.LogRecordCount(), "expected exactly one top query record")
	records := logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	namespace, ok := records.At(0).Attributes().Get("db.namespace")
	require.True(t, ok, "db.namespace attribute must be present")
	require.Equal(t, database, namespace.Str())
}
