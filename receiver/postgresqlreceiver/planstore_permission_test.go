// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package postgresqlreceiver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestQueryPlanStoreNoPermissionWarnsOnceNamesGrantAndResumes covers the monitoring user that cannot read
// the plan store: lookups stay off, the cause is reported once at the default log level, and they resume
// on the first collection after the privilege is granted.
func TestQueryPlanStoreNoPermissionWarnsOnceNamesGrantAndResumes(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	p := &postgreSQLScraper{logger: zap.New(core), storePlansUnavailable: make(map[string]storePlansMark)}
	c := &toggleStoreClient{status: planStoreNoPermission}
	t0 := time.Unix(1_700_000_000, 0)

	require.False(t, p.queryPlanStoreReady(t.Context(), c, "appdb", t0), "lookups must be skipped, a failed read would cache an empty plan")
	require.True(t, p.queryPlanStoreSkipped("appdb", t0), "other queries in the same collection must skip")
	warns := logs.FilterLevelExact(zap.WarnLevel).All()
	require.Len(t, warns, 1)
	require.Contains(t, warns[0].Message, "pg_store_plans_info")
	require.Contains(t, warns[0].Message, "grant SELECT")
	require.Equal(t, "appdb", warns[0].ContextMap()["database"])

	// Same cause on the next collection: checked again, no further warning.
	t1 := t0.Add(time.Minute)
	require.False(t, p.queryPlanStoreSkipped("appdb", t1), "each new collection must re-check")
	require.False(t, p.queryPlanStoreReady(t.Context(), c, "appdb", t1))
	require.Equal(t, 1, logs.FilterLevelExact(zap.WarnLevel).Len(), "a persistent permission error must warn once")

	// The privilege is granted while the collector keeps running: the next collection resumes.
	c.status = planStoreReady
	require.True(t, p.queryPlanStoreReady(t.Context(), c, "appdb", t1.Add(time.Minute)))
	require.NotContains(t, p.storePlansUnavailable, "appdb")
	require.Equal(t, 1, logs.FilterLevelExact(zap.InfoLevel).Len(), "resuming must be logged once")
}

// TestQueryPlanStoreCauseChangeWarnsAgain checks that moving from a missing extension to a permission
// error reports the new cause instead of staying silent.
func TestQueryPlanStoreCauseChangeWarnsAgain(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	p := &postgreSQLScraper{logger: zap.New(core), storePlansUnavailable: make(map[string]storePlansMark)}
	c := &toggleStoreClient{status: planStoreMissing}
	t0 := time.Unix(1_700_000_000, 0)

	require.False(t, p.queryPlanStoreReady(t.Context(), c, "appdb", t0))
	c.status = planStoreNoPermission
	require.False(t, p.queryPlanStoreReady(t.Context(), c, "appdb", t0.Add(time.Minute)))

	warns := logs.All()
	require.Len(t, warns, 2)
	require.Contains(t, warns[0].Message, "not installed")
	require.Contains(t, warns[1].Message, "cannot read it")
}
