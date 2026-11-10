// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package translator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/metadata"
)

func enableGate(t *testing.T, gate *featuregate.Gate) {
	prev := gate.IsEnabled()
	require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), true))
	t.Cleanup(func() {
		require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), prev))
	})
}

func TestClientSpanWithNewAndLegacyDBAttributesPrefersNew(t *testing.T) {
	enableGate(t, metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate)

	attributes := make(map[string]pcommon.Value)
	attributes["db.system.name"] = pcommon.NewValueStr("postgresql")
	attributes["db.namespace"] = pcommon.NewValueStr("orders")
	attributes["db.query.text"] = pcommon.NewValueStr("SELECT 1")
	attributes["user.name"] = pcommon.NewValueStr("new_user")
	attributes["db.system"] = pcommon.NewValueStr("mysql")
	attributes["db.name"] = pcommon.NewValueStr("customers")
	attributes["db.statement"] = pcommon.NewValueStr("SELECT 2")
	attributes["db.user"] = pcommon.NewValueStr("legacy_user")
	span := constructSQLSpan(attributes)

	// Map iteration order is random; repeat to make an order-dependent bug surface.
	for range 50 {
		_, sqlData := makeSQL(span, attributes)
		require.NotNil(t, sqlData)
		assert.Equal(t, "postgresql", *sqlData.DatabaseType)
		assert.Equal(t, "localhost/orders", *sqlData.ConnectionString)
		assert.Equal(t, "SELECT 1", *sqlData.SanitizedQuery)
		assert.Equal(t, "new_user", *sqlData.User)
	}
}

func TestClientSpanWithLegacyDBAttributesOnlyWithEmitV1(t *testing.T) {
	enableGate(t, metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate)

	attributes := make(map[string]pcommon.Value)
	attributes["db.system"] = pcommon.NewValueStr("mysql")
	attributes["db.name"] = pcommon.NewValueStr("customers")
	span := constructSQLSpan(attributes)

	_, sqlData := makeSQL(span, attributes)
	require.NotNil(t, sqlData)
	assert.Equal(t, "mysql", *sqlData.DatabaseType)
	assert.Equal(t, "localhost/customers", *sqlData.ConnectionString)
}

func TestClientSpanWithStatementAttribute(t *testing.T) {
	attributes := make(map[string]pcommon.Value)
	attributes["db.system"] = pcommon.NewValueStr("mysql")
	attributes["db.name"] = pcommon.NewValueStr("customers")
	attributes["db.statement"] = pcommon.NewValueStr("SELECT * FROM user WHERE user_id = ?")
	attributes["db.user"] = pcommon.NewValueStr("readonly_user")
	attributes["db.connection_string"] = pcommon.NewValueStr("mysql://db.example.com:3306")
	attributes["net.peer.name"] = pcommon.NewValueStr("db.example.com")
	attributes["net.peer.port"] = pcommon.NewValueStr("3306")
	span := constructSQLSpan(attributes)

	filtered, sqlData := makeSQL(span, attributes)

	assert.NotNil(t, filtered)
	assert.NotNil(t, sqlData)

	w := testWriters.borrow()
	require.NoError(t, w.Encode(sqlData))
	jsonStr := w.String()
	testWriters.release(w)
	assert.Contains(t, jsonStr, "mysql://db.example.com:3306/customers")
}

func TestClientSpanWithNonSQLDatabase(t *testing.T) {
	attributes := make(map[string]pcommon.Value)
	attributes["db.system"] = pcommon.NewValueStr("redis")
	attributes["db.name"] = pcommon.NewValueStr("0")
	attributes["db.statement"] = pcommon.NewValueStr("SET key value")
	attributes["db.user"] = pcommon.NewValueStr("readonly_user")
	attributes["db.connection_string"] = pcommon.NewValueStr("redis://db.example.com:3306")
	attributes["net.peer.name"] = pcommon.NewValueStr("db.example.com")
	attributes["net.peer.port"] = pcommon.NewValueStr("3306")
	span := constructSQLSpan(attributes)

	filtered, sqlData := makeSQL(span, attributes)
	assert.Nil(t, sqlData)
	assert.NotNil(t, filtered)
}

func TestClientSpanWithoutDBurlAttribute(t *testing.T) {
	attributes := make(map[string]pcommon.Value)
	attributes["db.system"] = pcommon.NewValueStr("postgresql")
	attributes["db.name"] = pcommon.NewValueStr("customers")
	attributes["db.statement"] = pcommon.NewValueStr("SELECT * FROM user WHERE user_id = ?")
	attributes["db.user"] = pcommon.NewValueStr("readonly_user")
	attributes["db.connection_string"] = pcommon.NewValueStr("")
	attributes["net.peer.name"] = pcommon.NewValueStr("db.example.com")
	attributes["net.peer.port"] = pcommon.NewValueStr("3306")
	span := constructSQLSpan(attributes)

	filtered, sqlData := makeSQL(span, attributes)
	assert.NotNil(t, filtered)
	assert.NotNil(t, sqlData)

	assert.Equal(t, "users.findUnique", *sqlData.URL)
}

func constructSQLSpan(attributes map[string]pcommon.Value) ptrace.Span {
	endTime := time.Now().Round(time.Second)
	startTime := endTime.Add(-90 * time.Second)

	// constructSpanAttributes() in segment_test accepts a map of interfaces...
	interfaceAttributes := make(map[string]any)
	for k, v := range attributes {
		interfaceAttributes[k] = v
	}
	spanAttributes := constructSpanAttributes(interfaceAttributes)

	span := ptrace.NewSpan()
	span.SetTraceID(newTraceID())
	span.SetSpanID(newSegmentID())
	span.SetParentSpanID(newSegmentID())
	span.SetName("users.findUnique")
	span.SetKind(ptrace.SpanKindClient)
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(startTime))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(endTime))

	status := ptrace.NewStatus()
	status.SetCode(0)
	status.SetMessage("OK")
	status.CopyTo(span.Status())

	spanAttributes.CopyTo(span.Attributes())
	return span
}

func TestMakeSQLV1Endpoint(t *testing.T) {
	enableGate(t, metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate)
	for testCaseName, tt := range map[string]struct {
		spanServerAddress                                    string
		useLegacySpanDBAttributes                            bool
		spanDBConnectionString                               string
		expectedSegmentConnectionString, expectedSegmentName string
	}{
		"v1_endpoint":                            {spanServerAddress: "db.example.com", expectedSegmentConnectionString: "postgresql://db.example.com:5432/orders", expectedSegmentName: "orders@db.example.com"},
		"ipv6_endpoint":                          {spanServerAddress: "2001:db8::1", expectedSegmentConnectionString: "postgresql://[2001:db8::1]:5432/orders", expectedSegmentName: "orders@2001:db8::1"},
		"v0_db_keys_with_v1_endpoint":            {spanServerAddress: "db.example.com", useLegacySpanDBAttributes: true, expectedSegmentConnectionString: "postgresql://db.example.com:5432/orders", expectedSegmentName: "orders@db.example.com"},
		"v0_connection_string_is_kept":           {spanServerAddress: "db.example.com", spanDBConnectionString: "mysql://legacy.example.com:3306", expectedSegmentConnectionString: "mysql://legacy.example.com:3306/orders", expectedSegmentName: "orders@db.example.com"},
		"empty_host_falls_back_to_v0_connection": {spanDBConnectionString: "mysql://legacy.example.com:3306", expectedSegmentConnectionString: "mysql://legacy.example.com:3306/orders", expectedSegmentName: "orders@legacy.example.com"},
	} {
		t.Run(testCaseName, func(t *testing.T) {
			spanAttributes := map[string]any{"db.system.name": "postgresql", "db.namespace": "orders", "db.query.text": "SELECT $1", "server.address": tt.spanServerAddress, "server.port": int64(5432)}
			if tt.useLegacySpanDBAttributes {
				delete(spanAttributes, "db.system.name")
				delete(spanAttributes, "db.namespace")
				delete(spanAttributes, "db.query.text")
				spanAttributes["db.system"] = "postgresql"
				spanAttributes["db.name"] = "orders"
				spanAttributes["db.statement"] = "SELECT $1"
			}
			if tt.spanDBConnectionString != "" {
				spanAttributes["db.connection_string"] = tt.spanDBConnectionString
			}
			span := constructHTTPClientSpan(spanAttributes)
			remainingSpanAttributesAfterHTTP, _ := makeHTTP(span)
			remainingSpanAttributesAfterSQL, segmentSQL := makeSQL(span, remainingSpanAttributesAfterHTTP)
			require.NotNil(t, segmentSQL)
			require.NotNil(t, segmentSQL.ConnectionString)
			assert.Equal(t, tt.expectedSegmentConnectionString, *segmentSQL.ConnectionString)
			// server.address is a shared network attribute owned by the HTTP/network gate.
			require.NotContains(t, remainingSpanAttributesAfterSQL, "server.address")
			segment, err := MakeSegment(span, pcommon.NewResource(), nil, false, nil, false)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedSegmentName, *segment.Name)
			assert.Equal(t, tt.expectedSegmentConnectionString, *segment.SQL.ConnectionString)
		})
	}
}

func TestMakeSQLV1DBWithoutEndpointUsesLocalhost(t *testing.T) {
	enableGate(t, metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate)
	span := constructHTTPClientSpan(map[string]any{
		"db.system.name": "postgresql",
		"db.namespace":   "orders",
	})
	remainingSpanAttributes, _ := makeHTTP(span)
	_, segmentSQL := makeSQL(span, remainingSpanAttributes)
	require.NotNil(t, segmentSQL)
	// Same fallback as legacy data without an endpoint, so old and new SDK data match.
	assert.Equal(t, "localhost/orders", *segmentSQL.ConnectionString)
}

func TestMakeSQLMixedConventionsRespectFeatureGates(t *testing.T) {
	for testCaseName, mode := range map[string]struct {
		EmitV1DBConventions     bool
		DontEmitV0DBConventions bool
	}{
		"default":   {EmitV1DBConventions: false, DontEmitV0DBConventions: false},
		"v0_and_v1": {EmitV1DBConventions: true, DontEmitV0DBConventions: false},
		"v1_only":   {EmitV1DBConventions: true, DontEmitV0DBConventions: true},
	} {
		t.Run(testCaseName, func(t *testing.T) {
			if mode.EmitV1DBConventions {
				enableGate(t, metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate)
			}
			if mode.DontEmitV0DBConventions {
				enableGate(t, metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate)
			}
			span := constructHTTPClientSpan(map[string]any{
				"db.system":            "mysql",
				"db.name":              "legacy-db",
				"db.statement":         "SELECT 2",
				"db.connection_string": "mysql://legacy.example.com:3306",
				"db.user":              "db-user",
				"db.system.name":       "postgresql",
				"db.namespace":         "orders",
				"db.query.text":        "SELECT $1",
				"db.query.summary":     "SELECT orders",
				"server.address":       "db.example.com",
				"server.port":          int64(5432),
			})
			remainingSpanAttributesAfterHTTP, _ := makeHTTP(span)
			remainingSpanAttributesAfterSQL, segmentSQL := makeSQL(span, remainingSpanAttributesAfterHTTP)
			require.NotNil(t, segmentSQL)
			require.Contains(t, remainingSpanAttributesAfterSQL, "db.query.summary")
			if !mode.EmitV1DBConventions {
				assert.Equal(t, "mysql", *segmentSQL.DatabaseType)
				assert.Equal(t, "SELECT 2", *segmentSQL.SanitizedQuery)
				assert.Equal(t, "mysql://legacy.example.com:3306/legacy-db", *segmentSQL.ConnectionString)
			} else {
				assert.Equal(t, "postgresql", *segmentSQL.DatabaseType)
				assert.Equal(t, "SELECT $1", *segmentSQL.SanitizedQuery)
				if mode.DontEmitV0DBConventions {
					assert.Equal(t, "postgresql://db.example.com:5432/orders", *segmentSQL.ConnectionString)
				} else {
					// db.connection_string has no current replacement, so it is kept when present.
					assert.Equal(t, "mysql://legacy.example.com:3306/orders", *segmentSQL.ConnectionString)
				}
			}
			if mode.DontEmitV0DBConventions {
				assert.Nil(t, segmentSQL.User)
			} else {
				assert.Equal(t, "db-user", *segmentSQL.User)
			}
		})
	}
}
