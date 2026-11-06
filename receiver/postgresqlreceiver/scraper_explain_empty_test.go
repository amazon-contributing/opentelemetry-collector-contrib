// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
)

// expectEmptyExplain registers the EXPLAIN round trip for a row added by
// addTopQueryRow: the EXPLAIN returns a result set with no rows, followed by the
// DEALLOCATE cleanup.
func expectEmptyExplain(mock sqlmock.Sqlmock, queryID string) {
	preparedName := "otel_" + strings.ReplaceAll(queryID, "-", "_")
	mock.ExpectQuery("/* otel-collector-ignore */ SET plan_cache_mode = force_generic_plan;" +
		"PREPARE " + preparedName + " AS " + topQueryText + ";" +
		"EXPLAIN(FORMAT JSON) EXECUTE " + preparedName + ";").
		WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}))
	mock.ExpectExec("/* otel-collector-ignore */ DEALLOCATE PREPARE " + preparedName).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

// databaseRequestRecorder is a postgreSQLClientFactory backed by a sqlmock
// connection that records the database name of every getClient call. The scraper
// calls getClient from a single goroutine, so the recorder needs no locking.
type databaseRequestRecorder struct {
	db        *sql.DB
	requested []string
}

func (f *databaseRequestRecorder) getClient(database string) (client, error) {
	f.requested = append(f.requested, database)
	return &postgreSQLClient{client: f.db, closeFn: func() error { return nil }}, nil
}

func (*databaseRequestRecorder) close() error { return nil }

// TestScrapeTopQueriesCachesEmptyExplainPlan drives scrapeTopQuery twice for a
// live query whose EXPLAIN returns no rows. The first scrape runs EXPLAIN and
// caches the empty plan (explainQuery returns "" with no error). The second
// scrape must serve that empty plan from queryPlanCache without running
// EXPLAIN again, and still report the query with an empty plan.
func TestScrapeTopQueriesCachesEmptyExplainPlan(t *testing.T) {
	const (
		queryID  = "114514"
		database = "app"
		interval = time.Minute
	)

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()

	factory := &databaseRequestRecorder{db: db}
	// A long TTL keeps the cached plan alive across both scrapes.
	scraper := newPostgreSQLScraper(newTopQueryTestSettings(t), newTopQueryTestConfig(), factory, newCache(30), newTTLCache[string](10, time.Minute))

	// First scrape: the top query, then EXPLAIN (a result set with no rows) and
	// its DEALLOCATE cleanup.
	first := sqlmock.NewRows(topQueryTestColumns)
	addTopQueryRow(first, database, queryID, "false", "123", "11000")
	mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(first)
	expectEmptyExplain(mock, queryID)

	logs, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, interval)
	require.NoError(t, err)
	requireTopQueryWithEmptyPlan(t, logs, queryID)

	cachedPlan, cached := scraper.queryPlanCache.Get(queryID + "-plan")
	require.True(t, cached, "the empty plan must be cached so EXPLAIN is not retried on every scrape")
	require.Empty(t, cachedPlan)

	// Second scrape: rewind the last collection time so the interval has elapsed,
	// and let the counters grow so the query is reported again. Only the top query
	// is expected. sqlmock would answer a repeated EXPLAIN with an error that the
	// scraper only logs, so the getClient check below is what catches it.
	scraper.lastExecutionTimestamp = scraper.lastExecutionTimestamp.Add(-interval)
	second := sqlmock.NewRows(topQueryTestColumns)
	addTopQueryRow(second, database, queryID, "false", "130", "12000")
	mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(second)

	logs, err = scraper.scrapeTopQuery(t.Context(), 31, 32, 33, interval)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	requireTopQueryWithEmptyPlan(t, logs, queryID)

	// getClient(database) is only called by the EXPLAIN path; the top query itself
	// uses the default database client. Count only the former, so unrelated changes
	// to how often the default client is requested cannot break this test.
	explainClients := 0
	for _, name := range factory.requested {
		if name == database {
			explainClients++
		}
	}
	require.Equal(t, 1, explainClients, "EXPLAIN must run on the first scrape only, not again on the second")
}

// requireTopQueryWithEmptyPlan asserts that logs hold exactly one top query
// event for queryID whose postgresql.query_plan attribute is present and empty.
func requireTopQueryWithEmptyPlan(t *testing.T, logs plog.Logs, queryID string) {
	t.Helper()
	// Without any record the builder emits no ResourceLogs, so check the count
	// first to fail with a message rather than an index-out-of-range panic.
	require.Equal(t, 1, logs.LogRecordCount(), "expected exactly one top query record")
	attrs := logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes()
	gotQueryID, ok := attrs.Get("postgresql.queryid")
	require.True(t, ok, "postgresql.queryid attribute must be present")
	require.Equal(t, queryID, gotQueryID.Str())
	plan, ok := attrs.Get("postgresql.query_plan")
	require.True(t, ok, "postgresql.query_plan attribute must be present even when empty")
	require.Empty(t, plan.Str())
}
