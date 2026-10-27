// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tj/assert"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/common/testutil"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/golden"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/plogtest"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/pmetrictest"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver/internal/metadata"
)

func TestUnsuccessfulScrape(t *testing.T) {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig().(*Config)
	cfg.Endpoint = "fake:11111"

	scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, newDefaultClientFactory(cfg), newCache(1), newTTLCache[string](1, time.Second))

	actualMetrics, err := scraper.scrape(t.Context())
	require.Error(t, err)

	require.NoError(t, pmetrictest.CompareMetrics(pmetric.NewMetrics(), actualMetrics))
}

func TestScraper(t *testing.T) {
	factory := new(mockClientFactory)
	factory.initMocks([]string{"otel"})

	runTest := func(separateSchemaAttr bool, file string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()

		cfg := createDefaultConfig().(*Config)
		cfg.Databases = []string{"otel"}
		cfg.Metrics.PostgresqlWalDelay.Enabled = true
		cfg.Metrics.PostgresqlDeadlocks.Enabled = true
		cfg.Metrics.PostgresqlTempFiles.Enabled = true
		cfg.Metrics.PostgresqlTempIo.Enabled = true
		cfg.Metrics.PostgresqlTupUpdated.Enabled = true
		cfg.Metrics.PostgresqlTupReturned.Enabled = true
		cfg.Metrics.PostgresqlTupFetched.Enabled = true
		cfg.Metrics.PostgresqlTupInserted.Enabled = true
		cfg.Metrics.PostgresqlTupDeleted.Enabled = true
		cfg.Metrics.PostgresqlBlksHit.Enabled = true
		cfg.Metrics.PostgresqlBlksRead.Enabled = true
		cfg.Metrics.PostgresqlSequentialScans.Enabled = true
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = true

		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, factory, newCache(1), newTTLCache[string](1, time.Second))

		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "otel", file)
		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "expected_schemaattr.yaml")
	runTest(false, "expected.yaml")
}

func TestScraperNoDatabaseSingle(t *testing.T) {
	factory := new(mockClientFactory)
	factory.initMocks([]string{"otel"})

	runTest := func(separateSchemaAttr bool, file, fileDefault string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()

		cfg := createDefaultConfig().(*Config)

		// Validate expected default config values and then enable all metrics
		require.False(t, cfg.Metrics.PostgresqlWalDelay.Enabled)
		cfg.Metrics.PostgresqlWalDelay.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDeadlocks.Enabled)
		cfg.Metrics.PostgresqlDeadlocks.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempFiles.Enabled)
		cfg.Metrics.PostgresqlTempFiles.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempIo.Enabled)
		cfg.Metrics.PostgresqlTempIo.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupUpdated.Enabled)
		cfg.Metrics.PostgresqlTupUpdated.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupReturned.Enabled)
		cfg.Metrics.PostgresqlTupReturned.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupFetched.Enabled)
		cfg.Metrics.PostgresqlTupFetched.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupInserted.Enabled)
		cfg.Metrics.PostgresqlTupInserted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupDeleted.Enabled)
		cfg.Metrics.PostgresqlTupDeleted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksHit.Enabled)
		cfg.Metrics.PostgresqlBlksHit.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksRead.Enabled)
		cfg.Metrics.PostgresqlBlksRead.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlSequentialScans.Enabled)
		cfg.Metrics.PostgresqlSequentialScans.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDatabaseLocks.Enabled)
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = true

		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, factory, newCache(1), newTTLCache[string](1, time.Second))
		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "otel", file)
		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))

		cfg.Metrics.PostgresqlWalDelay.Enabled = false
		cfg.Metrics.PostgresqlDeadlocks.Enabled = false
		cfg.Metrics.PostgresqlTempFiles.Enabled = false
		cfg.Metrics.PostgresqlTempIo.Enabled = false
		cfg.Metrics.PostgresqlTupUpdated.Enabled = false
		cfg.Metrics.PostgresqlTupReturned.Enabled = false
		cfg.Metrics.PostgresqlTupFetched.Enabled = false
		cfg.Metrics.PostgresqlTupInserted.Enabled = false
		cfg.Metrics.PostgresqlTupDeleted.Enabled = false
		cfg.Metrics.PostgresqlBlksHit.Enabled = false
		cfg.Metrics.PostgresqlBlksRead.Enabled = false
		cfg.Metrics.PostgresqlSequentialScans.Enabled = false
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = false

		scraper = newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, factory, newCache(1), newTTLCache[string](1, time.Second))
		actualMetrics, err = scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile = filepath.Join("testdata", "scraper", "otel", fileDefault)
		expectedMetrics, err = golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "expected_schemaattr.yaml", "expected_default_metrics_schemaattr.yaml")
	runTest(false, "expected.yaml", "expected_default_metrics.yaml")
}

func TestScraperNoDatabaseMultipleWithoutPreciseLag(t *testing.T) {
	factory := mockClientFactory{}
	factory.initMocks([]string{"otel", "open", "telemetry"})

	runTest := func(separateSchemaAttr bool, file string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()
		defer testutil.SetFeatureGateForTest(t, metadata.PostgresqlreceiverPreciselagmetricsFeatureGate, false)()

		cfg := createDefaultConfig().(*Config)

		// Validate expected default config values and then enable all metrics except wal delay
		require.False(t, cfg.Metrics.PostgresqlWalDelay.Enabled)
		require.False(t, cfg.Metrics.PostgresqlDeadlocks.Enabled)
		cfg.Metrics.PostgresqlDeadlocks.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempFiles.Enabled)
		cfg.Metrics.PostgresqlTempFiles.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempIo.Enabled)
		cfg.Metrics.PostgresqlTempIo.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupUpdated.Enabled)
		cfg.Metrics.PostgresqlTupUpdated.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupReturned.Enabled)
		cfg.Metrics.PostgresqlTupReturned.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupFetched.Enabled)
		cfg.Metrics.PostgresqlTupFetched.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupInserted.Enabled)
		cfg.Metrics.PostgresqlTupInserted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupDeleted.Enabled)
		cfg.Metrics.PostgresqlTupDeleted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksHit.Enabled)
		cfg.Metrics.PostgresqlBlksHit.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksRead.Enabled)
		cfg.Metrics.PostgresqlBlksRead.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlSequentialScans.Enabled)
		cfg.Metrics.PostgresqlSequentialScans.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDatabaseLocks.Enabled)
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = true
		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, &factory, newCache(1), newTTLCache[string](1, time.Second))

		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "multiple", file)
		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "expected_imprecise_lag_schemaattr.yaml")
	runTest(false, "expected_imprecise_lag.yaml")
}

func TestScraperNoDatabaseMultiple(t *testing.T) {
	factory := mockClientFactory{}
	factory.initMocks([]string{"otel", "open", "telemetry"})

	runTest := func(separateSchemaAttr bool, file string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()

		cfg := createDefaultConfig().(*Config)

		// Validate expected default config values and then enable all metrics
		require.False(t, cfg.Metrics.PostgresqlWalDelay.Enabled)
		cfg.Metrics.PostgresqlWalDelay.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDeadlocks.Enabled)
		cfg.Metrics.PostgresqlDeadlocks.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempFiles.Enabled)
		cfg.Metrics.PostgresqlTempFiles.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempIo.Enabled)
		cfg.Metrics.PostgresqlTempIo.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupUpdated.Enabled)
		cfg.Metrics.PostgresqlTupUpdated.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupReturned.Enabled)
		cfg.Metrics.PostgresqlTupReturned.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupFetched.Enabled)
		cfg.Metrics.PostgresqlTupFetched.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupInserted.Enabled)
		cfg.Metrics.PostgresqlTupInserted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupDeleted.Enabled)
		cfg.Metrics.PostgresqlTupDeleted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksHit.Enabled)
		cfg.Metrics.PostgresqlBlksHit.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksRead.Enabled)
		cfg.Metrics.PostgresqlBlksRead.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlSequentialScans.Enabled)
		cfg.Metrics.PostgresqlSequentialScans.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDatabaseLocks.Enabled)
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = true
		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, &factory, newCache(1), newTTLCache[string](1, time.Second))

		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "multiple", file)
		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)
		fmt.Println(actualMetrics.ResourceMetrics())
		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "expected_schemaattr.yaml")
	runTest(false, "expected.yaml")
}

func TestScraperWithResourceAttributeFeatureGate(t *testing.T) {
	factory := mockClientFactory{}
	factory.initMocks([]string{"otel", "open", "telemetry"})

	runTest := func(separateSchemaAttr bool, file string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()

		cfg := createDefaultConfig().(*Config)

		// Validate expected default config values and then enable all metrics
		require.False(t, cfg.Metrics.PostgresqlWalDelay.Enabled)
		cfg.Metrics.PostgresqlWalDelay.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDeadlocks.Enabled)
		cfg.Metrics.PostgresqlDeadlocks.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempFiles.Enabled)
		cfg.Metrics.PostgresqlTempFiles.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempIo.Enabled)
		cfg.Metrics.PostgresqlTempIo.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupUpdated.Enabled)
		cfg.Metrics.PostgresqlTupUpdated.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupReturned.Enabled)
		cfg.Metrics.PostgresqlTupReturned.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupFetched.Enabled)
		cfg.Metrics.PostgresqlTupFetched.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupInserted.Enabled)
		cfg.Metrics.PostgresqlTupInserted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupDeleted.Enabled)
		cfg.Metrics.PostgresqlTupDeleted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksHit.Enabled)
		cfg.Metrics.PostgresqlBlksHit.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksRead.Enabled)
		cfg.Metrics.PostgresqlBlksRead.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlSequentialScans.Enabled)
		cfg.Metrics.PostgresqlSequentialScans.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDatabaseLocks.Enabled)
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = true

		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, &factory, newCache(1), newTTLCache[string](1, time.Second))

		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "multiple", file)
		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "expected_schemaattr.yaml")
	runTest(false, "expected.yaml")
}

func TestScraperWithResourceAttributeFeatureGateSingle(t *testing.T) {
	factory := mockClientFactory{}
	factory.initMocks([]string{"otel"})

	runTest := func(separateSchemaAttr bool, file string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()

		cfg := createDefaultConfig().(*Config)

		// Validate expected default config values and then enable all metrics
		require.False(t, cfg.Metrics.PostgresqlWalDelay.Enabled)
		cfg.Metrics.PostgresqlWalDelay.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDeadlocks.Enabled)
		cfg.Metrics.PostgresqlDeadlocks.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempFiles.Enabled)
		cfg.Metrics.PostgresqlTempFiles.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTempIo.Enabled)
		cfg.Metrics.PostgresqlTempIo.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupUpdated.Enabled)
		cfg.Metrics.PostgresqlTupUpdated.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupReturned.Enabled)
		cfg.Metrics.PostgresqlTupReturned.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupFetched.Enabled)
		cfg.Metrics.PostgresqlTupFetched.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupInserted.Enabled)
		cfg.Metrics.PostgresqlTupInserted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlTupDeleted.Enabled)
		cfg.Metrics.PostgresqlTupDeleted.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksHit.Enabled)
		cfg.Metrics.PostgresqlBlksHit.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlBlksRead.Enabled)
		cfg.Metrics.PostgresqlBlksRead.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlSequentialScans.Enabled)
		cfg.Metrics.PostgresqlSequentialScans.Enabled = true
		require.False(t, cfg.Metrics.PostgresqlDatabaseLocks.Enabled)
		cfg.Metrics.PostgresqlDatabaseLocks.Enabled = true
		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, &factory, newCache(1), newTTLCache[string](1, time.Second))

		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "otel", file)
		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "expected_schemaattr.yaml")
	runTest(false, "expected.yaml")
}

func TestScraperExcludeDatabase(t *testing.T) {
	factory := mockClientFactory{}
	factory.initMocks([]string{"otel", "telemetry"})

	runTest := func(separateSchemaAttr bool, file string) {
		defer testutil.SetFeatureGateForTest(t, metadata.ReceiverPostgresqlSeparateSchemaAttrFeatureGate, separateSchemaAttr)()

		cfg := createDefaultConfig().(*Config)
		cfg.ExcludeDatabases = []string{"open"}

		scraper := newPostgreSQLScraper(receivertest.NewNopSettings(metadata.Type), cfg, &factory, newCache(1), newTTLCache[string](1, time.Second))

		actualMetrics, err := scraper.scrape(t.Context())
		require.NoError(t, err)

		expectedFile := filepath.Join("testdata", "scraper", "multiple", file)

		expectedMetrics, err := golden.ReadMetrics(expectedFile)
		require.NoError(t, err)

		require.NoError(t, pmetrictest.CompareMetrics(expectedMetrics, actualMetrics, pmetrictest.IgnoreResourceAttributeValue("service.instance.id"), pmetrictest.IgnoreResourceMetricsOrder(),
			pmetrictest.IgnoreMetricDataPointsOrder(), pmetrictest.IgnoreStartTimestamp(), pmetrictest.IgnoreTimestamp()))
	}

	runTest(true, "exclude_schemaattr.yaml")
	runTest(false, "exclude.yaml")
}

//go:embed testdata/scraper/query-sample/expectedSql.sql
var expectedScrapeSampleQuery string

var querySampleColumns = []string{
	querySampleColumnDatname,
	querySampleColumnUsename,
	querySampleColumnClientAddr,
	querySampleColumnClientHostname,
	querySampleColumnClientPort,
	querySampleColumnQueryStart,
	querySampleColumnWaitEventType,
	querySampleColumnWaitEvent,
	querySampleColumnQueryID,
	querySampleColumnPID,
	querySampleColumnApplicationName,
	querySampleColumnQueryStartTimestamp,
	querySampleColumnState,
	querySampleColumnQuery,
	querySampleColumnDurationMilliseconds,
}

func newQuerySampleRows(t *testing.T, values map[string]any) *sqlmock.Rows {
	t.Helper()

	rowValues := make([]driver.Value, len(querySampleColumns))
	for i, col := range querySampleColumns {
		if v, ok := values[col]; ok {
			rowValues[i] = v
			continue
		}
		rowValues[i] = ""
	}

	return sqlmock.NewRows(querySampleColumns).AddRow(rowValues...)
}

func TestScrapeQuerySample(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerQuerySample.Enabled = true
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	assert.NoError(t, err)

	defer db.Close()

	factory := mockSimpleClientFactory{
		db: db,
	}

	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	assert.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{
		Logger: logger,
	}
	scraper := newPostgreSQLScraper(settings, cfg, factory, newCache(1), newTTLCache[string](1, time.Second))
	scraper.newestQueryTimestamp = 123440.111
	mock.ExpectQuery(expectedScrapeSampleQuery).WillReturnRows(newQuerySampleRows(t, map[string]any{
		querySampleColumnDatname:              "postgres",
		querySampleColumnUsename:              "otelu",
		querySampleColumnClientAddr:           "11.4.5.14",
		querySampleColumnClientHostname:       "otel",
		querySampleColumnClientPort:           "114514",
		querySampleColumnQueryStart:           "2025-02-12T16:37:54.843+08:00",
		querySampleColumnQueryID:              "123131231231",
		querySampleColumnPID:                  "1450",
		querySampleColumnApplicationName:      "receiver",
		querySampleColumnQueryStartTimestamp:  "123445.123",
		querySampleColumnState:                "idle",
		querySampleColumnQuery:                "select * from pg_stat_activity where id = 32",
		querySampleColumnDurationMilliseconds: "1.2",
	}))
	actualLogs, err := scraper.scrapeQuerySamples(t.Context(), 30)
	assert.NoError(t, err)
	expectedFile := filepath.Join("testdata", "scraper", "query-sample", "expected.yaml")
	// Uncomment line below to re-generate expected logs.
	// golden.WriteLogs(t, expectedFile, actualLogs)
	expectedLogs, err := golden.ReadLogs(expectedFile)
	require.NoError(t, err)
	errs := plogtest.CompareLogs(expectedLogs, actualLogs, plogtest.IgnoreResourceAttributeValue("service.instance.id"), plogtest.IgnoreTimestamp())
	assert.NoError(t, errs)
}

func TestScrapeQuerySampleWithTraceparent(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerQuerySample.Enabled = true
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)

	defer db.Close()

	factory := mockSimpleClientFactory{
		db: db,
	}

	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	require.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{
		Logger: logger,
	}

	scraper := newPostgreSQLScraper(settings, cfg, factory, newCache(1), newTTLCache[string](1, time.Second))
	scraper.newestQueryTimestamp = 123440.111

	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	mock.ExpectQuery(expectedScrapeSampleQuery).WillReturnRows(newQuerySampleRows(t, map[string]any{
		querySampleColumnDatname:              "postgres",
		querySampleColumnUsename:              "otelu",
		querySampleColumnClientAddr:           "11.4.5.14",
		querySampleColumnClientHostname:       "otel",
		querySampleColumnClientPort:           "114514",
		querySampleColumnQueryStart:           "2025-02-12T16:37:54.843+08:00",
		querySampleColumnQueryID:              "123131231231",
		querySampleColumnPID:                  "1450",
		querySampleColumnApplicationName:      traceparent,
		querySampleColumnQueryStartTimestamp:  "123445.123",
		querySampleColumnState:                "idle",
		querySampleColumnQuery:                "select * from pg_stat_activity where id = 32",
		querySampleColumnDurationMilliseconds: "1.2",
	}))
	actualLogs, err := scraper.scrapeQuerySamples(t.Context(), 30)
	require.NoError(t, err)

	require.Equal(t, 1, actualLogs.ResourceLogs().Len())
	rl := actualLogs.ResourceLogs().At(0)
	require.Equal(t, 1, rl.ScopeLogs().Len())
	sl := rl.ScopeLogs().At(0)
	require.Equal(t, 1, sl.LogRecords().Len())
	lr := sl.LogRecords().At(0)

	require.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", lr.TraceID().String())
	require.Equal(t, "00f067aa0ba902b7", lr.SpanID().String())

	applicationName, ok := lr.Attributes().Get("postgresql.application_name")
	require.True(t, ok)
	require.Equal(t, traceparent, applicationName.Str())
}

//go:embed testdata/scraper/top-query/expectedSql.sql
var expectedScrapeTopQuery string

//go:embed testdata/scraper/top-query/expectedExplain.sql
var expectedExplain string

func TestScrapeTopQueries(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerTopQuery.Enabled = true
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	assert.NoError(t, err)

	defer db.Close()

	factory := mockSimpleClientFactory{
		db: db,
	}

	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	assert.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{
		Logger: logger,
	}

	queryid := "114514"
	expectedReturnedValue := map[string]string{
		"calls":               "123",
		"datname":             "postgres",
		"shared_blks_dirtied": "1111",
		"shared_blks_hit":     "1112",
		"shared_blks_read":    "1113",
		"shared_blks_written": "1114",
		"local_blks_hit":      "2001",
		"local_blks_read":     "2002",
		"local_blks_dirtied":  "2003",
		"local_blks_written":  "2004",
		"temp_blks_read":      "1115",
		"temp_blks_written":   "1116",
		"query":               "select * from pg_stat_activity where id = 32",
		"queryid":             queryid,
		"rolname":             "master",
		"rolsuper":            "false",
		"rows":                "30",
		"total_exec_time":     "11000",
		"total_plan_time":     "12000",
	}

	expectedRows := make([]string, 0, len(expectedReturnedValue))
	var expectedValuesBuilder strings.Builder
	for k, v := range expectedReturnedValue {
		expectedRows = append(expectedRows, k)
		expectedValuesBuilder.WriteString(fmt.Sprintf("%s,", v))
	}
	expectedValues := expectedValuesBuilder.String()

	scraper := newPostgreSQLScraper(settings, cfg, factory, newCache(30), newTTLCache[string](1, time.Second))
	scraper.cache.Add(queryid+totalExecTimeColumnName, 10)
	scraper.cache.Add(queryid+totalPlanTimeColumnName, 11)
	scraper.cache.Add(queryid+callsColumnName, 120)
	scraper.cache.Add(queryid+rowsColumnName, 20)

	scraper.cache.Add(queryid+sharedBlksDirtiedColumnName, 1110)
	scraper.cache.Add(queryid+sharedBlksHitColumnName, 1110)
	scraper.cache.Add(queryid+sharedBlksReadColumnName, 1110)
	scraper.cache.Add(queryid+sharedBlksWrittenColumnName, 1110)
	scraper.cache.Add(queryid+tempBlksReadColumnName, 1110)
	scraper.cache.Add(queryid+tempBlksWrittenColumnName, 1110)

	mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(sqlmock.NewRows(expectedRows).FromCSVString(expectedValues[:len(expectedValues)-1]))
	mock.ExpectBegin()
	mock.ExpectExec(testSetPlanFormatSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(expectedExplain).WithArgs(queryid).WillReturnRows(sqlmock.NewRows([]string{"plan"}).AddRow("[{\"Plan\":{\"Node Type\":\"Merge Join\",\"Parallel Aware\":false,\"Async Capable\":false,\"Join Type\":\"Inner\",\"Startup Cost\":0.43,\"Total Cost\":55.27,\"Plan Rows\":290,\"Plan Width\":1675,\"Inner Unique\":\"?\",\"Merge Cond\":\"( e.businessentityid = p.businessentityid )\",\"Plans\":[{\"Node Type\":\"Index Scan\",\"Parent Relationship\":\"Outer\",\"Parallel Aware\":false,\"Async Capable\":false,\"Scan Direction\":\"Forward\",\"Index Name\":\"PK_Employee_BusinessEntityID\",\"Relation Name\":\"employee\",\"Alias\":\"e\",\"Startup Cost\":0.15,\"Total Cost\":21.5,\"Plan Rows\":290,\"Plan Width\":112},{\"Node Type\":\"Index Scan\",\"Parent Relationship\":\"Inner\",\"Parallel Aware\":false,\"Async Capable\":false,\"Scan Direction\":\"Forward\",\"Index Name\":\"PK_Person_BusinessEntityID\",\"Relation Name\":\"person\",\"Alias\":\"p\",\"Startup Cost\":0.29,\"Total Cost\":2261.87,\"Plan Rows\":19972,\"Plan Width\":1563}]}}]"))
	mock.ExpectCommit()
	actualLogs, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
	assert.NoError(t, err)
	expectedFile := filepath.Join("testdata", "scraper", "top-query", "expected.yaml")
	expectedLogs, err := golden.ReadLogs(expectedFile)
	require.NoError(t, err)
	// golden.WriteLogs(t, expectedFile, actualLogs)
	errs := plogtest.CompareLogs(expectedLogs, actualLogs, plogtest.IgnoreResourceAttributeValue("service.instance.id"), plogtest.IgnoreTimestamp())
	assert.NoError(t, errs)

	// Verify the cache has updated with latest counter

	calls, callsExists := scraper.cache.Get(queryid + callsColumnName)
	assert.True(t, callsExists)
	assert.Equal(t, float64(123), calls)
	execTime, execTimeExists := scraper.cache.Get(queryid + totalExecTimeColumnName)
	assert.True(t, execTimeExists)
	assert.Equal(t, float64(11), execTime)
	planTime, planTimeExists := scraper.cache.Get(queryid + totalPlanTimeColumnName)
	assert.True(t, planTimeExists)
	assert.Equal(t, float64(12), planTime)
}

func TestScrapeTopQueriesSkipsExplainForSuperuserQueries(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerTopQuery.Enabled = true
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	assert.NoError(t, err)

	defer db.Close()

	factory := mockSimpleClientFactory{db: db}

	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	assert.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{Logger: logger}

	queryid := "114514"
	returnedValues := map[string]string{
		"calls":               "123",
		"datname":             "postgres",
		"shared_blks_dirtied": "1111",
		"shared_blks_hit":     "1112",
		"shared_blks_read":    "1113",
		"shared_blks_written": "1114",
		"local_blks_hit":      "2001",
		"local_blks_read":     "2002",
		"local_blks_dirtied":  "2003",
		"local_blks_written":  "2004",
		"temp_blks_read":      "1115",
		"temp_blks_written":   "1116",
		"query":               "select * from pg_stat_activity where id = 32",
		"queryid":             queryid,
		"rolname":             "postgres",
		"rolsuper":            "true",
		"rows":                "30",
		"total_exec_time":     "11000",
		"total_plan_time":     "12000",
	}

	rowNames := make([]string, 0, len(returnedValues))
	var valuesBuilder strings.Builder
	for k, v := range returnedValues {
		rowNames = append(rowNames, k)
		valuesBuilder.WriteString(fmt.Sprintf("%s,", v))
	}
	values := valuesBuilder.String()

	scraper := newPostgreSQLScraper(settings, cfg, factory, newCache(30), newTTLCache[string](10, time.Minute))

	// Only the top query itself is expected. Deliberately no EXPLAIN expectation.
	mock.ExpectQuery(expectedScrapeTopQuery).WillReturnRows(sqlmock.NewRows(rowNames).FromCSVString(values[:len(values)-1]))

	actualLogs, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
	assert.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	// A failed EXPLAIN attempt would still cache an empty plan, so an absent cache
	// entry is what distinguishes "skipped" from "attempted and failed".
	_, cached := scraper.queryPlanCache.Get(queryid + "-plan")
	assert.False(t, cached, "no EXPLAIN should have been attempted for a superuser query")

	// The query is still reported, just without a plan.
	records := actualLogs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	require.Equal(t, 1, records.Len())
	planAttr, exists := records.At(0).Attributes().Get("postgresql.query_plan")
	require.True(t, exists)
	assert.Empty(t, planAttr.Str())
}

func TestScrapeTopQueriesCollectsOnlyWhenIntervalHasElapsed(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Databases = []string{}
	cfg.Events.DbServerTopQuery.Enabled = true
	cfg.TopQueryCollection.CollectionInterval = 600 * time.Second
	db, _, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	assert.NoError(t, err)

	defer db.Close()

	factory := mockSimpleClientFactory{
		db: db,
	}

	settings := receivertest.NewNopSettings(metadata.Type)
	logger, err := zap.NewProduction()
	assert.NoError(t, err)
	settings.TelemetrySettings = component.TelemetrySettings{
		Logger: logger,
	}

	scraper := newPostgreSQLScraper(settings, cfg, factory, newCache(30), newTTLCache[string](1, time.Second))

	assert.True(t, scraper.lastExecutionTimestamp.IsZero(), "lastExecutionTimestamp should be zero before first collection")
	logs1, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
	assert.NotNil(t, logs1)
	assert.NoError(t, err)
	assert.False(t, scraper.lastExecutionTimestamp.IsZero(), "lastExecutionTimestamp won't be zero after first collection")

	collectionTime := scraper.lastExecutionTimestamp
	logs2, err := scraper.scrapeTopQuery(t.Context(), 31, 32, 33, time.Minute)
	assert.NotNil(t, logs2)
	assert.NoError(t, err)
	assert.Equal(t, collectionTime, scraper.lastExecutionTimestamp, "No new collection should happen until configured collection_interval")
}

func TestIsCollectionDue(t *testing.T) {
	collectionInterval := 20 * time.Second
	currentCollectionTime := time.Now()

	logger, err := zap.NewProduction()
	require.NoError(t, err)
	scrpr := postgreSQLScraper{
		// setting lastExecutionTimestamp to be past 'collectionInterval'
		lastExecutionTimestamp: currentCollectionTime.Add(-collectionInterval),
		logger:                 logger,
	}
	isCollectionDue := scrpr.isCollectionDue(currentCollectionTime, collectionInterval)
	assert.True(t, isCollectionDue, "lastExecutionTimestamp is older than collection_interval, so collection should be due.")

	scrpr.lastExecutionTimestamp = currentCollectionTime.Add(-10 * time.Second)
	isCollectionDue = scrpr.isCollectionDue(currentCollectionTime, collectionInterval)
	assert.False(t, isCollectionDue, "collection_interval is not yet reached since lastExecutionTimestamp, so collection is not due.")
}

const testCapturedQueryPlanSQL = "/* otel-collector-ignore */ SELECT plan FROM pg_store_plans WHERE queryid::text = $1 ORDER BY calls DESC LIMIT 1"

const testSetPlanFormatSQL = "SET LOCAL pg_store_plans.plan_format = 'json'"

func TestExplainQuery(t *testing.T) {
	testCases := []struct {
		name         string
		query        string
		queryID      string
		capturedPlan string // object form, as pg_store_plans returns it
		wantPlan     string // array form, as historically emitted
	}{
		{
			name:         "numeric queryID",
			query:        "SELECT * FROM users",
			queryID:      "12345",
			capturedPlan: `{"Plan":{"Node Type":"Seq Scan","Relation Name":"users"}}`,
			wantPlan:     `[{"Plan":{"Node Type":"Seq Scan","Relation Name":"users"}}]`,
		},
		{
			name:         "parameterized query",
			query:        "SELECT * FROM orders WHERE user_id = $1",
			queryID:      "12347",
			capturedPlan: `{"Plan":{"Node Type":"Index Scan","Relation Name":"orders"}}`,
			wantPlan:     `[{"Plan":{"Node Type":"Index Scan","Relation Name":"orders"}}]`,
		},
		{
			name:         "hyphenated queryID",
			query:        "SELECT * FROM products WHERE id = $1",
			queryID:      "abc-def-123",
			capturedPlan: `{"Plan":{"Node Type":"Index Scan","Relation Name":"products"}}`,
			wantPlan:     `[{"Plan":{"Node Type":"Index Scan","Relation Name":"products"}}]`,
		},
	}

	logger, err := zap.NewProduction()
	require.NoError(t, err)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, dbErr := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, dbErr)
			defer db.Close()

			client := &postgreSQLClient{
				client:  db,
				closeFn: func() error { return nil },
			}

			mock.ExpectBegin()
			mock.ExpectExec(testSetPlanFormatSQL).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(testCapturedQueryPlanSQL).WithArgs(tc.queryID).WillReturnRows(
				sqlmock.NewRows([]string{"plan"}).AddRow(tc.capturedPlan),
			)
			mock.ExpectCommit()

			plan, planErr := client.explainQuery(tc.query, tc.queryID, logger)
			require.NoError(t, planErr)
			assert.Equal(t, tc.wantPlan, plan)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestExplainQueryNoPlan(t *testing.T) {
	logger, err := zap.NewProduction()
	require.NoError(t, err)

	t.Run("no rows returns empty plan without error", func(t *testing.T) {
		db, mock, dbErr := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, dbErr)
		defer db.Close()

		mock.ExpectBegin()
		mock.ExpectExec(testSetPlanFormatSQL).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(testCapturedQueryPlanSQL).WithArgs("99999").WillReturnRows(
			sqlmock.NewRows([]string{"plan"}),
		)
		mock.ExpectRollback()

		c := &postgreSQLClient{client: db, closeFn: func() error { return nil }}
		plan, planErr := c.explainQuery("SELECT 1", "99999", logger)
		require.NoError(t, planErr)
		assert.Empty(t, plan)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error returns empty plan without error", func(t *testing.T) {
		db, mock, dbErr := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, dbErr)
		defer db.Close()

		mock.ExpectBegin()
		mock.ExpectExec(testSetPlanFormatSQL).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery(testCapturedQueryPlanSQL).WithArgs("99999").WillReturnError(errors.New("connection reset"))
		mock.ExpectRollback()

		c := &postgreSQLClient{client: db, closeFn: func() error { return nil }}
		plan, planErr := c.explainQuery("SELECT 1", "99999", logger)
		require.NoError(t, planErr)
		assert.Empty(t, plan)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestLogTopQueryErrOnce(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	p := &postgreSQLScraper{logger: zap.New(core)}

	p.logTopQueryErrOnce("failed to get top query", errors.New("boom"))
	p.logTopQueryErrOnce("failed to get top query", errors.New("boom"))
	require.Equal(t, 1, logs.Len(), "the same persistent error must log only once, not every poll")

	p.clearTopQueryErr()
	p.logTopQueryErrOnce("failed to get top query", errors.New("boom"))
	require.Equal(t, 2, logs.Len(), "an error after a successful collection must be reported again")
}

// storeAbsentClient reports pg_store_plans as not installed.
type storeAbsentClient struct{ *mockClient }

func (storeAbsentClient) queryPlanStoreAvailable(context.Context) (bool, error) { return false, nil }

func TestQueryPlanStoreReadyWarnsOnceAndSkips(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	p := &postgreSQLScraper{logger: zap.New(core), storePlansMissing: make(map[string]bool)}

	ready := p.queryPlanStoreReady(context.Background(), storeAbsentClient{}, "appdb")
	require.False(t, ready, "plan lookups must be skipped when pg_store_plans is absent")
	require.True(t, p.storePlansMissing["appdb"], "the database must be marked so later polls skip it")
	require.Equal(t, 1, logs.Len(), "a missing extension must warn once")
}

func TestCollectSessionStates(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	cfg.Metrics.PostgresqlSessions.Enabled = true

	factory := new(mockClientFactory)
	factory.initMocks([]string{"otel"})

	settings := receivertest.NewNopSettings(metadata.Type)
	scraper := newPostgreSQLScraper(settings, cfg, factory, newCache(1), newTTLCache[string](1, time.Second))

	actualMetrics, err := scraper.scrape(t.Context())
	require.NoError(t, err)

	found := false
	for i := 0; i < actualMetrics.ResourceMetrics().Len(); i++ {
		rm := actualMetrics.ResourceMetrics().At(i)
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			sm := rm.ScopeMetrics().At(j)
			for k := 0; k < sm.Metrics().Len(); k++ {
				if sm.Metrics().At(k).Name() == "postgresql.sessions" {
					found = true
				}
			}
		}
	}
	assert.True(t, found, "postgresql.sessions metric should be present when enabled")
}

type (
	mockClientFactory       struct{ mock.Mock }
	mockClient              struct{ mock.Mock }
	mockSimpleClientFactory struct {
		db *sql.DB
	}
)

// explainQuery implements client.
func (*mockClient) explainQuery(string, string, *zap.Logger) (string, error) {
	panic("unimplemented")
}

// queryPlanStoreAvailable implements client.
func (*mockClient) queryPlanStoreAvailable(context.Context) (bool, error) {
	return true, nil
}

// getTopQuery implements client.
func (*mockClient) getTopQuery(context.Context, int64, *zap.Logger) ([]map[string]any, error) {
	panic("unimplemented")
}

// close implements postgreSQLClientFactory.
func (mockSimpleClientFactory) close() error {
	return nil
}

// getClient implements postgreSQLClientFactory.
func (m mockSimpleClientFactory) getClient(string) (client, error) {
	return &postgreSQLClient{
		client:  m.db,
		closeFn: m.close,
	}, nil
}

// getQuerySamples implements client.
func (*mockClient) getQuerySamples(context.Context, int64, float64, *zap.Logger) ([]map[string]any, float64, error) {
	panic("this should not be invoked")
}

var _ client = &mockClient{}

func (m *mockClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockClient) getDatabaseStats(_ context.Context, databases []string) (map[databaseName]databaseStats, error) {
	args := m.Called(databases)
	return args.Get(0).(map[databaseName]databaseStats), args.Error(1)
}

func (m *mockClient) getDatabaseLocks(ctx context.Context) ([]databaseLocks, error) {
	args := m.Called(ctx)
	return args.Get(0).([]databaseLocks), args.Error(1)
}

func (m *mockClient) getBackends(_ context.Context, databases []string) (map[databaseName]int64, error) {
	args := m.Called(databases)
	return args.Get(0).(map[databaseName]int64), args.Error(1)
}

func (m *mockClient) getDatabaseSize(_ context.Context, databases []string) (map[databaseName]int64, error) {
	args := m.Called(databases)
	return args.Get(0).(map[databaseName]int64), args.Error(1)
}

func (m *mockClient) getDatabaseTableMetrics(ctx context.Context, database string) (map[tableIdentifier]tableStats, error) {
	args := m.Called(ctx, database)
	return args.Get(0).(map[tableIdentifier]tableStats), args.Error(1)
}

func (m *mockClient) getBlocksReadByTable(ctx context.Context, database string) (map[tableIdentifier]tableIOStats, error) {
	args := m.Called(ctx, database)
	return args.Get(0).(map[tableIdentifier]tableIOStats), args.Error(1)
}

func (m *mockClient) getIndexStats(ctx context.Context, database string) (map[indexIdentifer]indexStat, error) {
	args := m.Called(ctx, database)
	return args.Get(0).(map[indexIdentifer]indexStat), args.Error(1)
}

func (m *mockClient) getFunctionStats(ctx context.Context, database string) (map[functionIdentifer]functionStat, error) {
	args := m.Called(ctx, database)
	return args.Get(0).(map[functionIdentifer]functionStat), args.Error(1)
}

func (m *mockClient) getBGWriterStats(ctx context.Context) (*bgStat, error) {
	args := m.Called(ctx)
	return args.Get(0).(*bgStat), args.Error(1)
}

func (m *mockClient) getMaxConnections(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockClient) getLatestWalAgeSeconds(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockClient) getReplicationStats(ctx context.Context) ([]replicationStats, error) {
	args := m.Called(ctx)
	return args.Get(0).([]replicationStats), args.Error(1)
}

func (m *mockClient) listDatabases(_ context.Context) ([]string, error) {
	args := m.Called()
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockClient) getVersion(_ context.Context) (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockClient) getSessionStates(ctx context.Context) (map[string]int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(map[string]int64), args.Error(1)
}

func (m *mockClientFactory) getClient(database string) (client, error) {
	args := m.Called(database)
	return args.Get(0).(client), args.Error(1)
}

func (m *mockClientFactory) close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockClientFactory) initMocks(databases []string) {
	listClient := new(mockClient)
	listClient.initMocks(defaultPostgreSQLDatabase, "public", databases, 0)
	m.On("getClient", defaultPostgreSQLDatabase).Return(listClient, nil)

	for index, db := range databases {
		client := new(mockClient)
		client.initMocks(db, "public", databases, index)
		m.On("getClient", db).Return(client, nil)
	}
}

func (m *mockClient) initMocks(database, schema string, databases []string, index int) {
	m.On("Close").Return(nil)

	if database == defaultPostgreSQLDatabase {
		m.On("listDatabases").Return(databases, nil)

		dbStats := map[databaseName]databaseStats{}
		dbSize := map[databaseName]int64{}
		backends := map[databaseName]int64{}

		for idx, db := range databases {
			dbStats[databaseName(db)] = databaseStats{
				transactionCommitted: int64(idx + 1),
				transactionRollback:  int64(idx + 2),
				deadlocks:            int64(idx + 3),
				tempFiles:            int64(idx + 4),
				tupUpdated:           int64(idx + 5),
				tupReturned:          int64(idx + 6),
				tupFetched:           int64(idx + 7),
				tupInserted:          int64(idx + 8),
				tupDeleted:           int64(idx + 9),
				blksHit:              int64(idx + 10),
				blksRead:             int64(idx + 11),
				tempIo:               int64(idx + 12),
			}
			dbSize[databaseName(db)] = int64(idx + 4)
			backends[databaseName(db)] = int64(idx + 3)
		}

		m.On("getDatabaseStats", databases).Return(dbStats, nil)
		m.On("getDatabaseSize", databases).Return(dbSize, nil)
		m.On("getBackends", databases).Return(backends, nil)
		m.On("getBGWriterStats", mock.Anything).Return(&bgStat{
			checkpointsReq:       1,
			checkpointsScheduled: 2,
			checkpointWriteTime:  3.12,
			checkpointSyncTime:   4.23,
			bgWrites:             5,
			bufferBackendWrites:  7,
			bufferFsyncWrites:    8,
			bufferCheckpoints:    9,
			buffersAllocated:     10,
			maxWritten:           11,
		}, nil)
		m.On("getMaxConnections", mock.Anything).Return(int64(100), nil)
		m.On("getLatestWalAgeSeconds", mock.Anything).Return(int64(3600), nil)
		m.On("getDatabaseLocks", mock.Anything).Return([]databaseLocks{
			{
				relation: "pg_locks",
				mode:     "AccessShareLock",
				lockType: "relation",
				locks:    3600,
			},
			{
				relation: "pg_class",
				mode:     "AccessShareLock",
				lockType: "relation",
				locks:    5600,
			},
		}, nil)
		m.On("getDatabaseLocks", mock.Anything).Return([]databaseLocks{
			{
				relation: "abd_table",
				mode:     "ExplicitLock",
				lockType: "relation",
				locks:    1600,
			},
			{
				relation: "pg_class",
				mode:     "AccessShareLock",
				lockType: "relation",
				locks:    5600,
			},
		}, errors.New("some error"))
		m.On("getReplicationStats", mock.Anything).Return([]replicationStats{
			{
				clientAddr:   "unix",
				pendingBytes: 1024,
				flushLagInt:  600,
				replayLagInt: 700,
				writeLagInt:  800,
				flushLag:     600.400,
				replayLag:    700.550,
				writeLag:     800.660,
			},
			{
				clientAddr:   "nulls",
				pendingBytes: -1,
				flushLagInt:  -1,
				replayLagInt: -1,
				writeLagInt:  -1,
				flushLag:     -1,
				replayLag:    -1,
				writeLag:     -1,
			},
		}, nil)
		m.On("getSessionStates", mock.Anything).Return(map[string]int64{
			"active":                        3,
			"idle":                          5,
			"idle in transaction":           1,
			"idle in transaction (aborted)": 0,
		}, nil)
	} else {
		table1 := "table1"
		table2 := "table2"
		tableMetrics := map[tableIdentifier]tableStats{
			tableKey(database, schema, table1): {
				database:    database,
				schema:      schema,
				table:       table1,
				live:        int64(index + 7),
				dead:        int64(index + 8),
				inserts:     int64(index + 39),
				upd:         int64(index + 40),
				del:         int64(index + 41),
				hotUpd:      int64(index + 42),
				size:        int64(index + 43),
				vacuumCount: int64(index + 44),
				seqScans:    int64(index + 45),
			},
			tableKey(database, schema, table2): {
				database:    database,
				schema:      schema,
				table:       table2,
				live:        int64(index + 9),
				dead:        int64(index + 10),
				inserts:     int64(index + 43),
				upd:         int64(index + 44),
				del:         int64(index + 45),
				hotUpd:      int64(index + 46),
				size:        int64(index + 47),
				vacuumCount: int64(index + 48),
				seqScans:    int64(index + 49),
			},
		}

		blocksMetrics := map[tableIdentifier]tableIOStats{
			tableKey(database, schema, table1): {
				database:  database,
				schema:    schema,
				table:     table1,
				heapRead:  int64(index + 19),
				heapHit:   int64(index + 20),
				idxRead:   int64(index + 21),
				idxHit:    int64(index + 22),
				toastRead: int64(index + 23),
				toastHit:  int64(index + 24),
				tidxRead:  int64(index + 25),
				tidxHit:   int64(index + 26),
			},
			tableKey(database, schema, table2): {
				database:  database,
				schema:    schema,
				table:     table2,
				heapRead:  int64(index + 27),
				heapHit:   int64(index + 28),
				idxRead:   int64(index + 29),
				idxHit:    int64(index + 30),
				toastRead: int64(index + 31),
				toastHit:  int64(index + 32),
				tidxRead:  int64(index + 33),
				tidxHit:   int64(index + 34),
			},
		}

		m.On("getDatabaseTableMetrics", mock.Anything, database).Return(tableMetrics, nil)
		m.On("getBlocksReadByTable", mock.Anything, database).Return(blocksMetrics, nil)

		index1 := database + "_test1_pkey"
		index2 := database + "_test2_pkey"
		indexStats := map[indexIdentifer]indexStat{
			indexKey(database, schema, table1, index1): {
				database: database,
				schema:   schema,
				table:    table1,
				index:    index1,
				scans:    int64(index + 35),
				size:     int64(index + 36),
			},
			indexKey(index2, schema, table2, index2): {
				database: database,
				schema:   schema,
				table:    table2,
				index:    index2,
				scans:    int64(index + 37),
				size:     int64(index + 38),
			},
		}
		m.On("getIndexStats", mock.Anything, database).Return(indexStats, nil)

		function1 := "test_function1"
		function2 := "test_function2"
		functionStats := map[functionIdentifer]functionStat{
			functionKey(database, schema, function1): {
				database: database,
				schema:   schema,
				function: function1,
				calls:    int64(index + 50),
			},
			functionKey(database, schema, function2): {
				database: database,
				schema:   schema,
				function: function2,
				calls:    int64(index + 51),
			},
		}
		m.On("getFunctionStats", mock.Anything, database).Return(functionStats, nil)
	}
}

func TestGetInstanceId(t *testing.T) {
	localhostName, _ := os.Hostname()

	instanceString := "example.com:5432"
	instanceID := getInstanceID(instanceString, zap.NewNop())
	assert.Equal(t, "example.com:5432", instanceID)

	localHostStringUppercase := "Localhost:5432"
	localInstanceID := getInstanceID(localHostStringUppercase, zap.NewNop())
	assert.NotNil(t, localInstanceID)
	assert.Equal(t, localhostName+":5432", localInstanceID)

	localHostString := "127.0.0.1:5432"
	localInstanceID = getInstanceID(localHostString, zap.NewNop())
	assert.NotNil(t, localInstanceID)
	assert.Equal(t, localhostName+":5432", localInstanceID)

	localHostStringIPV6 := "[::1]:5432"
	localInstanceID = getInstanceID(localHostStringIPV6, zap.NewNop())
	assert.NotNil(t, localInstanceID)
	assert.Equal(t, localhostName+":5432", localInstanceID)

	hostNameErrorSample := ""
	localInstanceID = getInstanceID(hostNameErrorSample, zap.NewNop())
	assert.NotNil(t, localInstanceID)
	assert.Equal(t, "unknown:5432", localInstanceID)
}

// TestExplainQueryObfuscatesCapturedPlanLiterals feeds pg_store_plans-style output (object
// form, with a literal in Sort Key / Group Key) and verifies the literal is obfuscated out
// and the emitted plan keeps the historical [{...}] array shape.
func TestExplainQueryObfuscatesCapturedPlanLiterals(t *testing.T) {
	logger := zap.NewNop()
	cases := []struct {
		name         string
		capturedPlan string
		literal      string
		key          string
	}{
		{
			name:         "sort key literal",
			capturedPlan: `{"Plan":{"Node Type":"Sort","Sort Key":["(CASE WHEN (email = 'alice.secret@example.com'::text) THEN 0 ELSE 1 END)","email"],"Plans":[{"Node Type":"Seq Scan","Relation Name":"users"}]}}`,
			literal:      "alice.secret@example.com",
			key:          "Sort Key",
		},
		{
			name:         "group key literal",
			capturedPlan: `{"Plan":{"Node Type":"Aggregate","Group Key":["(email = 'bob.secret@example.com'::text)"],"Plans":[{"Node Type":"Seq Scan","Relation Name":"users"}]}}`,
			literal:      "bob.secret@example.com",
			key:          "Group Key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, tc.capturedPlan, tc.literal)

			db, mock, dbErr := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, dbErr)
			defer db.Close()

			mock.ExpectBegin()
			mock.ExpectExec(testSetPlanFormatSQL).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(testCapturedQueryPlanSQL).WithArgs("1").WillReturnRows(
				sqlmock.NewRows([]string{"plan"}).AddRow(tc.capturedPlan),
			)
			mock.ExpectCommit()

			c := &postgreSQLClient{client: db, closeFn: func() error { return nil }}
			plan, err := c.explainQuery("", "1", logger)
			require.NoError(t, err)
			assert.NotContains(t, plan, tc.literal, "captured literal leaked into the emitted plan")
			assert.True(t, strings.HasPrefix(plan, "["), "emitted plan should keep the array shape")
			assert.Contains(t, plan, tc.key)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
