// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package mysqlreceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mysqlreceiver"

import (
	"github.com/DataDog/datadog-agent/pkg/obfuscate"
)

var (
	obfuscateSQLConfig = obfuscate.SQLConfig{DBMS: "mysql"}
	obfuscatorConfig   = obfuscate.Config{
		SQLExecPlan: defaultSQLPlanObfuscateSettings,
	}
)

type obfuscator struct{ inner *obfuscate.Obfuscator }

func newObfuscator() *obfuscator {
	return &obfuscator{inner: obfuscate.NewObfuscator(obfuscatorConfig)}
}

func (o *obfuscator) obfuscateSQLString(sql string) (string, error) {
	res, err := o.inner.ObfuscateSQLStringWithOptions(sql, &obfuscateSQLConfig)
	if err != nil {
		return "", err
	}
	return res.Query, nil
}

func (o *obfuscator) obfuscatePlan(plan string) (string, error) {
	return o.inner.ObfuscateSQLExecPlan(plan, false)
}

// For further information, see https://dev.mysql.com/doc/refman/8.4/en/explain.html
// MySQL 8.4 EXPLAIN FORMAT=JSON produces two formats depending on explain_json_format_version:
//   - Version 1 (default): query_block → ordering_operation → table → attached_condition
//   - Version 2: top-level query + inputs array, each node has condition/operation/access_type etc.
var defaultSQLPlanObfuscateSettings = obfuscate.JSONConfig{
	Enabled: true,
	ObfuscateSQLValues: []string{
		"query",
		"condition",
		"operation",
		"attached_condition",
	},
	// KeepValues is an allowlist. New structural keys added by MySQL EXPLAIN
	// output must be added here to avoid silent obfuscation. Review after
	// MySQL minor version upgrades.
	KeepValues: []string{
		"cost_info",
		"ordering_operation",
		"query_block",
		"query_plan",
		"query_type",
		"select_id",
		"table",
		"used_columns",
		"using_filesort",
		"access_type",
		"covering",
		"estimated_rows",
		"estimated_total_cost",
		"filter_columns",
		"index_access_type",
		"index_name",
		"inputs",
		"json_schema_version",
		"limit",
		"limit_offset",
		"per_chunk_limit",
		"ranges",
		"row_ids",
		"schema_name",
		"sort_fields",
		"table_name",
	},
}
