// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestExplainQueryEmptyResult drives explainQuery with an explainable query and
// an EXPLAIN mock that returns zero rows. explainQuery must return an empty plan
// and no error instead of indexing into the empty result.
func TestExplainQueryEmptyResult(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer db.Close()

	logger, err := zap.NewProduction()
	require.NoError(t, err)

	pgClient := &postgreSQLClient{
		client:  db,
		closeFn: func() error { return nil },
	}

	const queryID = "99999"
	query := "SELECT * FROM users WHERE id = $1"
	expectedSQL := "/* otel-collector-ignore */ SET plan_cache_mode = force_generic_plan;" +
		"PREPARE otel_99999 AS SELECT * FROM users WHERE id = $1;" +
		"EXPLAIN(FORMAT JSON) EXECUTE otel_99999(null);"

	// EXPLAIN returns a result set with the QUERY PLAN column but no rows.
	mock.ExpectQuery(expectedSQL).WillReturnRows(sqlmock.NewRows([]string{"QUERY PLAN"}))

	plan, err := pgClient.explainQuery(query, queryID, logger)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Empty(t, plan, "an empty EXPLAIN result must yield an empty plan, not a panic")
}
