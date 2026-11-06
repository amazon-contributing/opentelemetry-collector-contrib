// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package translator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	awsxray "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/xray"
)

// The attribute sets below are copied from spans that real ADOT SDKs emitted in an end-to-end run
// (ADOT Java 1.33 vs 2.32, ADOT Python 0.22 with and without OTEL_SEMCONV_STABILITY_OPT_IN).
// With the EmitV1 gates enabled, old-SDK and new-SDK data for the same call must produce the same
// X-Ray fields.

func emitV1Gates() map[*featuregate.Gate]bool {
	return map[*featuregate.Gate]bool{
		httpDomain.emitV1: true,
		dbDomain.emitV1:   true,
		rpcDomain.emitV1:  true,
	}
}

func allV1Gates() map[*featuregate.Gate]bool {
	gates := emitV1Gates()
	gates[httpDomain.dontEmitV0] = true
	gates[dbDomain.dontEmitV0] = true
	gates[rpcDomain.dontEmitV0] = true
	return gates
}

type sdkSpan struct {
	kind  ptrace.SpanKind
	attrs map[string]any
}

func sqlSummary(s *awsxray.Segment) map[string]string {
	return map[string]string{
		"name":              deref(s.Name),
		"connection_string": deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.ConnectionString })),
		"database_type":     deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.DatabaseType })),
		"sanitized_query":   deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.SanitizedQuery })),
	}
}

func TestSDKConsistencyJavaJDBC(t *testing.T) {
	old := sdkSpan{ptrace.SpanKindClient, map[string]any{ // ADOT Java 1.33
		"db.connection_string": "postgresql://postgres:5432", "db.name": "e2e", "db.operation": "SELECT",
		"db.sql.table": "items", "db.statement": "SELECT id, name FROM items WHERE id = ?", "db.system": "postgresql",
		"db.user": "e2e", "net.peer.name": "postgres", "net.peer.port": 5432,
	}}
	newDefault := sdkSpan{ptrace.SpanKindClient, map[string]any{ // ADOT Java 2.32 (default: legacy DB keys + server.*)
		"db.connection_string": "postgresql://postgres:5432", "db.name": "e2e", "db.operation": "SELECT",
		"db.sql.table": "items", "db.statement": "SELECT id, name FROM items WHERE id = ?", "db.system": "postgresql",
		"db.user": "e2e", "server.address": "postgres", "server.port": 5432,
	}}
	newStable := sdkSpan{ptrace.SpanKindClient, map[string]any{ // ADOT Java 2.32 with OTEL_SEMCONV_STABILITY_OPT_IN=database
		"db.namespace": "e2e|e2e", "db.query.summary": "SELECT items",
		"db.query.text": "SELECT id, name FROM items WHERE id = ?", "db.system.name": "postgresql", "server.address": "postgres",
	}}
	want := map[string]string{
		"name": "e2e@postgres", "connection_string": "postgresql://postgres:5432/e2e",
		"database_type": "postgresql", "sanitized_query": "SELECT id, name FROM items WHERE id = ?",
	}
	setGates(t, emitV1Gates())
	for name, in := range map[string]sdkSpan{"old": old, "new_default": newDefault, "new_stable": newStable} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, sqlSummary(makeSDKSegment(t, in)))
		})
	}
	t.Run("new_stable_with_DontEmitV0", func(t *testing.T) {
		setGates(t, allV1Gates())
		assert.Equal(t, want, sqlSummary(makeSDKSegment(t, newStable)))
	})
}

func TestSDKConsistencyPythonPsycopg2(t *testing.T) {
	old := sdkSpan{ptrace.SpanKindClient, map[string]any{
		"db.name": "e2e", "db.statement": "SELECT id, name FROM items WHERE id = %s", "db.system": "postgresql",
		"db.user": "e2e", "net.peer.name": "postgres", "net.peer.port": 5432,
	}}
	stable := sdkSpan{ptrace.SpanKindClient, map[string]any{
		"db.namespace": "e2e", "db.query.text": "SELECT id, name FROM items WHERE id = %s", "db.system.name": "postgresql",
		"server.address": "postgres", "server.port": 5432,
	}}
	want := map[string]string{
		"name": "e2e@postgres", "connection_string": "postgresql://postgres:5432/e2e",
		"database_type": "postgresql", "sanitized_query": "SELECT id, name FROM items WHERE id = %s",
	}
	setGates(t, emitV1Gates())
	assert.Equal(t, want, sqlSummary(makeSDKSegment(t, old)), "old")
	assert.Equal(t, want, sqlSummary(makeSDKSegment(t, stable)), "stable")
}

func TestSDKConsistencyHTTPClientName(t *testing.T) {
	setGates(t, emitV1Gates())
	for name, in := range map[string]sdkSpan{
		"python_requests_old": {ptrace.SpanKindClient, map[string]any{
			"http.method": "GET", "http.status_code": 200, "http.url": "http://downstream:8081/items/1?verbose=true",
		}},
		"python_requests_new": {ptrace.SpanKindClient, map[string]any{
			"http.request.method": "GET", "http.response.status_code": 200, "network.peer.address": "downstream",
			"network.peer.port": 8081, "server.address": "downstream", "server.port": 8081,
			"url.full": "http://downstream:8081/items/1?verbose=true",
		}},
		"java_http_client_old": {ptrace.SpanKindClient, map[string]any{
			"http.method": "GET", "http.status_code": 200, "http.url": "http://downstream:8081/items/1?verbose=true",
			"net.peer.name": "downstream", "net.peer.port": 8081,
		}},
		"java_http_client_new": {ptrace.SpanKindClient, map[string]any{
			"http.request.method": "GET", "http.response.status_code": 200, "server.address": "downstream",
			"server.port": 8081, "url.full": "http://downstream:8081/items/1?verbose=true",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			segment := makeSDKSegment(t, in)
			assert.Equal(t, "downstream", deref(segment.Name))
			assert.Equal(t, "http://downstream:8081/items/1?verbose=true", httpURL(segment))
			assert.Equal(t, "GET", deref(httpRequest(segment, func(r *awsxray.RequestData) *string { return r.Method })))
			assert.Equal(t, "200", httpStatus(segment))
		})
	}
}

func TestSDKConsistencyHTTPServerURL(t *testing.T) {
	setGates(t, emitV1Gates())
	for name, in := range map[string]sdkSpan{
		"python_flask_old": {ptrace.SpanKindServer, map[string]any{
			"http.method": "GET", "http.host": "python:8080", "http.scheme": "http", "http.server_name": "0.0.0.0",
			"http.status_code": 200, "http.target": "/run", "net.host.name": "python:8080", "net.host.port": 8080,
			"net.peer.ip": "10.0.0.9",
		}},
		// Python's new-semconv WSGI instrumentation copies the Host header (host:port) into server.address.
		"python_flask_new": {ptrace.SpanKindServer, map[string]any{
			"client.address": "10.0.0.9", "http.request.method": "GET", "http.response.status_code": 200,
			"server.address": "python:8080", "server.port": 8080, "url.path": "/run", "url.scheme": "http",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			segment := makeSDKSegment(t, in)
			assert.Equal(t, "http://python:8080/run", httpURL(segment))
			assert.Equal(t, "10.0.0.9", deref(httpRequest(segment, func(r *awsxray.RequestData) *string { return r.ClientIP })))
		})
	}
}

func TestSDKConsistencyJavaServerClientIP(t *testing.T) {
	setGates(t, emitV1Gates())
	for name, in := range map[string]sdkSpan{
		"java_tomcat_old": {ptrace.SpanKindServer, map[string]any{ // ADOT Java 1.33
			"http.method": "GET", "http.route": "/run", "http.scheme": "http", "http.status_code": 200, "http.target": "/run",
			"net.host.name": "java", "net.host.port": 8080, "net.sock.peer.addr": "10.0.0.9", "net.sock.peer.port": 41000,
			"user_agent.original": "curl/8",
		}},
		"java_tomcat_new": {ptrace.SpanKindServer, map[string]any{ // ADOT Java 2.32
			"client.address": "10.0.0.9", "http.request.method": "GET", "http.response.status_code": 200, "http.route": "/run",
			"network.peer.address": "10.0.0.9", "network.peer.port": 41000, "server.address": "java", "server.port": 8080,
			"url.path": "/run", "url.scheme": "http", "user_agent.original": "curl/8",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			segment := makeSDKSegment(t, in)
			assert.Equal(t, "http://java:8080/run", httpURL(segment))
			assert.Equal(t, "10.0.0.9", deref(httpRequest(segment, func(r *awsxray.RequestData) *string { return r.ClientIP })))
			assert.Nil(t, segment.HTTP.Request.XForwardedFor)
		})
	}
}

func TestSDKConsistencyPythonBotocore(t *testing.T) {
	// botocore emits the same attributes in old and new mode: legacy http.status_code plus server.*.
	in := sdkSpan{ptrace.SpanKindClient, map[string]any{
		"aws.region": "us-west-2", "http.status_code": 400, "rpc.method": "DescribeTable", "rpc.service": "DynamoDB",
		"rpc.system": "aws-api", "server.address": "dynamodb.us-west-2.amazonaws.com", "server.port": 443,
		"aws.dynamodb.table_names": []string{"t"},
	}}
	for name, gates := range map[string]map[*featuregate.Gate]bool{"EmitV1": emitV1Gates(), "EmitV1+DontEmitV0": allV1Gates()} {
		t.Run(name, func(t *testing.T) {
			setGates(t, gates)
			span := buildSDKSpan(in)
			span.Status().SetCode(ptrace.StatusCodeError)
			segment := makeSDKSegmentFromSpan(t, span)
			assert.Equal(t, "https://dynamodb.us-west-2.amazonaws.com/", httpURL(segment))
			assert.Equal(t, "400", httpStatus(segment))
			// A 4xx AWS call stays a client error even when legacy HTTP keys are otherwise ignored.
			assert.True(t, *segment.Error)
			assert.False(t, *segment.Fault)
			assert.Equal(t, "DescribeTable", deref(awsField(segment, func(a *awsxray.AWSData) *string { return a.Operation })))
		})
	}
}

func TestSegmentNameIsSanitized(t *testing.T) {
	for name, tt := range map[string]struct {
		gates map[*featuregate.Gate]bool
		attrs map[string]any
		want  string
	}{
		"postgres namespace with schema":                            {emitV1Gates(), map[string]any{"db.system.name": "postgresql", "db.namespace": "orders|public", "server.address": "db"}, "orders@db"},
		"sql server instance and database":                          {emitV1Gates(), map[string]any{"db.system.name": "microsoft.sql_server", "db.namespace": "MSSQLSERVER|orders", "server.address": "db"}, "orders@db"},
		"invalid characters from attributes are removed by default": {nil, map[string]any{"peer.service": "bad|peer"}, "badpeer"},
	} {
		t.Run(name, func(t *testing.T) {
			setGates(t, tt.gates)
			assert.Equal(t, tt.want, deref(makeSDKSegment(t, sdkSpan{ptrace.SpanKindClient, tt.attrs}).Name))
		})
	}
}

func TestSQSQueueURLPrefersCurrentConvention(t *testing.T) {
	in := sdkSpan{ptrace.SpanKindProducer, map[string]any{
		"aws.queue_url": "https://sqs/legacy-xray", "messaging.url": "https://sqs/legacy",
		"aws.sqs.queue.url": "https://sqs/current", "rpc.system": "aws-api",
	}}
	setGates(t, emitV1Gates())
	segment := makeSDKSegment(t, in)
	assert.Equal(t, "https://sqs/current", deref(awsField(segment, func(a *awsxray.AWSData) *string { return a.QueueURL })))
}

func TestSDKConsistencyPythonBoto3SQSMessagingSpan(t *testing.T) {
	// boto3sqs reports the queue URL only as messaging.url, in old and new mode alike.
	in := sdkSpan{ptrace.SpanKindProducer, map[string]any{
		"messaging.destination": "q", "messaging.destination_kind": "queue", "messaging.system": "aws.sqs",
		"messaging.url": "https://sqs.us-west-2.amazonaws.com/1/q",
	}}
	for name, gates := range map[string]map[*featuregate.Gate]bool{"EmitV1": emitV1Gates(), "EmitV1+DontEmitV0": allV1Gates()} {
		t.Run(name, func(t *testing.T) {
			setGates(t, gates)
			segment := makeSDKSegment(t, in)
			assert.Equal(t, "https://sqs.us-west-2.amazonaws.com/1/q", deref(awsField(segment, func(a *awsxray.AWSData) *string { return a.QueueURL })))
		})
	}
}

func buildSDKSpan(in sdkSpan) ptrace.Span {
	span := ptrace.NewSpan()
	span.SetTraceID(newTraceID())
	span.SetSpanID(newSegmentID())
	span.SetParentSpanID(newSegmentID())
	span.SetName("operation")
	span.SetKind(in.kind)
	constructSpanAttributes(in.attrs).CopyTo(span.Attributes())
	return span
}

func makeSDKSegment(t *testing.T, in sdkSpan) *awsxray.Segment {
	t.Helper()
	return makeSDKSegmentFromSpan(t, buildSDKSpan(in))
}

func makeSDKSegmentFromSpan(t *testing.T, span ptrace.Span) *awsxray.Segment {
	t.Helper()
	segment, err := MakeSegment(span, pcommon.NewResource(), nil, false, nil, false)
	require.NoError(t, err)
	return segment
}
