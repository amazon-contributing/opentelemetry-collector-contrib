// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package translator

import (
	"cmp"
	"maps"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/metadata"
	awsxray "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/xray"
)

// migrationDomain groups the paired EmitV1/DontEmitV0 gates of one semconv domain.
type migrationDomain struct {
	name       string
	emitV1     *featuregate.Gate
	dontEmitV0 *featuregate.Gate
}

var (
	httpDomain = migrationDomain{
		name:       "http",
		emitV1:     metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate,
		dontEmitV0: metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate,
	}
	dbDomain = migrationDomain{
		name:       "db",
		emitV1:     metadata.ExporterAwsxrayEmitV1DBConventionsFeatureGate,
		dontEmitV0: metadata.ExporterAwsxrayDontEmitV0DBConventionsFeatureGate,
	}
	rpcDomain = migrationDomain{
		name:       "rpc",
		emitV1:     metadata.ExporterAwsxrayEmitV1RPCConventionsFeatureGate,
		dontEmitV0: metadata.ExporterAwsxrayDontEmitV0RPCConventionsFeatureGate,
	}
	allMigrationGates = []*featuregate.Gate{
		httpDomain.emitV1, httpDomain.dontEmitV0,
		dbDomain.emitV1, dbDomain.dontEmitV0,
		rpcDomain.emitV1, rpcDomain.dontEmitV0,
	}
)

// semconvMigrationCase describes one X-Ray field that is populated from either
// the v0 (legacy) or the v1 (current) form of a semantic convention. The v0 and
// v1 inputs deliberately carry different values so the winner is observable.
type semconvMigrationCase struct {
	domain migrationDomain
	kind   ptrace.SpanKind
	// v0 and v1 are span attributes; v0Event and v1Event are attributes of a single span event.
	v0, v1           map[string]any
	v0Event, v1Event map[string]any
	get              func(*awsxray.Segment) string
	want0, want1     string
	// wantBothEmitV1 overrides want1 for v0+v1 input under EmitV1 (for v0 keys with no v1 replacement).
	wantBothEmitV1 string
	// v0Supported marks v0 inputs that intentionally remain translated when DontEmitV0 is enabled.
	v0Supported bool
}

func semconvMigrationCases() map[string]semconvMigrationCase {
	return map[string]semconvMigrationCase{
		"http/request_method": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"http.method": "GET"},
			v1: map[string]any{"http.request.method": "POST"},
			get: func(s *awsxray.Segment) string {
				return deref(httpRequest(s, func(r *awsxray.RequestData) *string { return r.Method }))
			},
			want0: "GET", want1: "POST",
		},
		"http/response_status_code": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"http.status_code": 200},
			v1:    map[string]any{"http.response.status_code": 201},
			get:   httpStatus,
			want0: "200", want1: "201",
		},
		"http/user_agent": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"http.user_agent": "legacy-agent"},
			v1: map[string]any{"user_agent.original": "new-agent"},
			get: func(s *awsxray.Segment) string {
				return deref(httpRequest(s, func(r *awsxray.RequestData) *string { return r.UserAgent }))
			},
			want0: "legacy-agent", want1: "new-agent",
		},
		"http/full_url": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"http.url": "https://legacy.example.com/a"},
			v1:    map[string]any{"url.full": "https://new.example.com/b"},
			get:   httpURL,
			want0: "https://legacy.example.com/a", want1: "https://new.example.com/b",
		},
		"http/client_url_from_parts": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{
				"http.method": "GET", "http.scheme": "https", "net.peer.name": "legacy.example.com",
				"net.peer.port": 8443, "http.target": "/legacy?x=1",
			},
			v1: map[string]any{
				"http.request.method": "GET", "url.scheme": "https", "server.address": "new.example.com",
				"server.port": 9443, "url.path": "/new", "url.query": "y=2",
			},
			get:   httpURL,
			want0: "https://legacy.example.com:8443/legacy?x=1", want1: "https://new.example.com:9443/new?y=2",
		},
		"http/server_url_from_parts": {
			domain: httpDomain, kind: ptrace.SpanKindServer,
			v0: map[string]any{
				"http.method": "GET", "http.scheme": "https", "net.host.name": "legacy.example.com",
				"net.host.port": 8443, "http.target": "/legacy",
			},
			v1: map[string]any{
				"http.request.method": "GET", "url.scheme": "https", "server.address": "new.example.com",
				"server.port": 9443, "url.path": "/new",
			},
			get:   httpURL,
			want0: "https://legacy.example.com:8443/legacy", want1: "https://new.example.com:9443/new",
		},
		"http/client_ip": {
			domain: httpDomain, kind: ptrace.SpanKindServer,
			v0: map[string]any{"http.method": "GET", "http.client_ip": "10.0.0.1"},
			v1: map[string]any{"http.request.method": "GET", "client.address": "10.0.0.2"},
			get: func(s *awsxray.Segment) string {
				return deref(httpRequest(s, func(r *awsxray.RequestData) *string { return r.ClientIP }))
			},
			want0: "10.0.0.1", want1: "10.0.0.2",
		},
		"http/peer_ip": {
			domain: httpDomain, kind: ptrace.SpanKindServer,
			v0: map[string]any{"http.method": "GET", "net.peer.ip": "10.0.0.3"},
			v1: map[string]any{"http.request.method": "GET", "network.peer.address": "10.0.0.4"},
			get: func(s *awsxray.Segment) string {
				return deref(httpRequest(s, func(r *awsxray.RequestData) *string { return r.ClientIP }))
			},
			want0: "10.0.0.3", want1: "10.0.0.4",
		},
		"http/segment_name_from_http_host": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"http.host": "legacy.example.com"},
			v1:    map[string]any{"server.address": "new.example.com"},
			get:   segmentName,
			want0: "legacy.example.com", want1: "new.example.com",
		},
		"http/segment_name_from_net_peer_name": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"net.peer.name": "legacy.example.com"},
			v1:    map[string]any{"server.address": "new.example.com"},
			get:   segmentName,
			want0: "legacy.example.com", want1: "new.example.com",
		},
		"http/received_message_size": {
			domain: httpDomain, kind: ptrace.SpanKindClient,
			v0:      map[string]any{"http.method": "GET"},
			v1:      map[string]any{"http.request.method": "GET"},
			v0Event: map[string]any{"message.type": "RECEIVED", "messaging.message_payload_size_bytes": 10},
			v1Event: map[string]any{"message.type": "RECEIVED", "messaging.message.body.size": 20},
			get:     httpContentLength,
			want0:   "10", want1: "20",
		},
		"http/sqs_queue_url": {
			domain: httpDomain, kind: ptrace.SpanKindProducer,
			v0: map[string]any{"messaging.url": "https://sqs.us-east-1.amazonaws.com/1/legacy"},
			v1: map[string]any{"aws.sqs.queue.url": "https://sqs.us-east-1.amazonaws.com/1/new"},
			get: func(s *awsxray.Segment) string {
				return deref(awsField(s, func(a *awsxray.AWSData) *string { return a.QueueURL }))
			},
			want0: "https://sqs.us-east-1.amazonaws.com/1/legacy", want1: "https://sqs.us-east-1.amazonaws.com/1/new",
		},
		"db/system": {
			domain: dbDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"db.system": "mssql"},
			v1: map[string]any{"db.system.name": "microsoft.sql_server"},
			get: func(s *awsxray.Segment) string {
				return deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.DatabaseType }))
			},
			// Renamed systems keep the legacy X-Ray database_type so old and new SDK data match.
			want0: "mssql", want1: "mssql",
		},
		"db/statement": {
			domain: dbDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"db.system": "mysql", "db.statement": "SELECT legacy"},
			v1: map[string]any{"db.system.name": "mysql", "db.query.text": "SELECT new"},
			get: func(s *awsxray.Segment) string {
				return deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.SanitizedQuery }))
			},
			want0: "SELECT legacy", want1: "SELECT new",
		},
		"db/connection_string": {
			domain: dbDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"db.system": "mysql", "db.name": "legacy_db", "db.connection_string": "jdbc:mysql://legacy.example.com:3306"},
			v1: map[string]any{"db.system.name": "mysql", "db.namespace": "new_db", "server.address": "new.example.com", "server.port": 5432},
			get: func(s *awsxray.Segment) string {
				return deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.ConnectionString }))
			},
			want0: "jdbc:mysql://legacy.example.com:3306/legacy_db", want1: "mysql://new.example.com:5432/new_db",
			// db.connection_string has no v1 replacement, so it is kept while v0 keys are read.
			wantBothEmitV1: "jdbc:mysql://legacy.example.com:3306/new_db",
		},
		"db/segment_name": {
			domain: dbDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"db.system": "mysql", "db.name": "legacy_db", "db.connection_string": "jdbc:mysql://legacy.example.com:3306"},
			v1:    map[string]any{"db.system.name": "mysql", "db.namespace": "new_db", "server.address": "new.example.com"},
			get:   segmentName,
			want0: "legacy_db@legacy.example.com", want1: "new_db@new.example.com",
		},
		"db/user": {
			domain: dbDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"db.system": "mysql", "db.user": "legacy_user"},
			v1: map[string]any{"db.system.name": "mysql", "user.name": "new_user"},
			get: func(s *awsxray.Segment) string {
				return deref(sqlField(s, func(q *awsxray.SQLData) *string { return q.User }))
			},
			want0: "legacy_user", want1: "new_user",
		},
		"rpc/generic_service_name": {
			domain: rpcDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"rpc.system": "grpc", "rpc.service": "legacy.Greeter", "rpc.method": "SayHello"},
			v1:    map[string]any{"rpc.system.name": "grpc", "rpc.method": "new.Greeter/SayHello"},
			get:   segmentName,
			want0: "legacy.Greeter", want1: "new.Greeter",
		},
		"rpc/peer_service": {
			domain: rpcDomain, kind: ptrace.SpanKindClient,
			v0:    map[string]any{"peer.service": "legacy-peer"},
			v1:    map[string]any{"service.peer.name": "new-peer"},
			get:   segmentName,
			want0: "legacy-peer", want1: "new-peer",
		},
		"rpc/aws_sdk_operation": {
			domain: rpcDomain, kind: ptrace.SpanKindClient,
			v0: map[string]any{"rpc.system": "aws-api", "rpc.service": "DynamoDB", "rpc.method": "GetItem"},
			v1: map[string]any{"rpc.system.name": "aws-api", "rpc.method": "DynamoDB/PutItem"},
			get: func(s *awsxray.Segment) string {
				return deref(awsField(s, func(a *awsxray.AWSData) *string { return a.Operation }))
			},
			want0: "GetItem", want1: "PutItem",
			v0Supported: true,
		},
	}
}

// TestSemconvMigrationMatrix verifies, for every migrated field, that:
//   - default gates translate v0 input as before,
//   - EmitV1 translates v0 and v1 input and prefers v1 when both are present,
//   - EmitV1+DontEmitV0 ignores v0 input (keeping it as metadata) and translates v1.
//
// Run with -count to shake out attribute-order dependence.
func TestSemconvMigrationMatrix(t *testing.T) {
	type gateState struct {
		name               string
		emitV1, dontEmitV0 bool
	}
	states := []gateState{
		{name: "default"},
		{name: "EmitV1", emitV1: true},
		{name: "EmitV1+DontEmitV0", emitV1: true, dontEmitV0: true},
	}
	for caseName, tc := range semconvMigrationCases() {
		for _, st := range states {
			t.Run(caseName+"/"+st.name, func(t *testing.T) {
				setGates(t, map[*featuregate.Gate]bool{tc.domain.emitV1: st.emitV1, tc.domain.dontEmitV0: st.dontEmitV0})

				segV0 := makeMigrationSegment(t, tc, tc.v0, tc.v0Event)
				segV1 := makeMigrationSegment(t, tc, tc.v1, tc.v1Event)
				segBoth := makeMigrationSegment(t, tc, union(tc.v0, tc.v1), union(tc.v0Event, tc.v1Event))

				switch {
				case !st.emitV1:
					assert.Equal(t, tc.want0, tc.get(segV0), "v0 input")
				case !st.dontEmitV0:
					assert.Equal(t, tc.want0, tc.get(segV0), "v0 input")
					assert.Equal(t, tc.want1, tc.get(segV1), "v1 input")
					assert.Equal(t, cmp.Or(tc.wantBothEmitV1, tc.want1), tc.get(segBoth), "v0+v1 input must prefer v1")
				default:
					if tc.v0Supported {
						assert.Equal(t, tc.want0, tc.get(segV0), "v0 input that stays supported")
					} else {
						assert.NotEqual(t, tc.want0, tc.get(segV0), "v0 input must not be translated")
						for key, value := range tc.v0 {
							if _, shared := tc.v1[key]; shared {
								continue // key exists in both conventions (e.g. rpc.method), so it is not a v0-only key
							}
							assert.Contains(t, defaultMetadata(segV0), key, "ignored v0 attribute must be kept as metadata")
							if got, ok := defaultMetadata(segV0)[key]; ok {
								assert.EqualValues(t, value, got)
							}
						}
					}
					assert.Equal(t, tc.want1, tc.get(segV1), "v1 input")
					assert.Equal(t, tc.want1, tc.get(segBoth), "v0+v1 input must prefer v1")
				}
			})
		}
	}
}

// TestSemconvMigrationGatesAreIsolated verifies that enabling the DB or RPC gates does
// not change the segment produced for another domain's input. HTTP/network gates are
// excluded as the "other" domain because server.address/port are shared by DB and RPC spans.
func TestSemconvMigrationGatesAreIsolated(t *testing.T) {
	domains := []migrationDomain{dbDomain, rpcDomain}
	for caseName, tc := range semconvMigrationCases() {
		for _, other := range domains {
			if other.name == tc.domain.name {
				continue
			}
			t.Run(caseName+"/with_"+other.name+"_gates", func(t *testing.T) {
				for _, input := range []struct {
					name        string
					attrs, evts map[string]any
				}{
					{"v0", tc.v0, tc.v0Event},
					{"v1", tc.v1, tc.v1Event},
				} {
					setGates(t, nil)
					want := segmentDocument(t, makeMigrationSegment(t, tc, input.attrs, input.evts))
					setGates(t, map[*featuregate.Gate]bool{other.emitV1: true, other.dontEmitV0: true})
					got := segmentDocument(t, makeMigrationSegment(t, tc, input.attrs, input.evts))
					assert.JSONEq(t, want, got, "%s input changed by %s gates", input.name, other.name)
				}
			})
		}
	}
}

func makeMigrationSegment(t *testing.T, tc semconvMigrationCase, attrs, eventAttrs map[string]any) *awsxray.Segment {
	t.Helper()
	span := ptrace.NewSpan()
	span.SetTraceID(newTraceID())
	span.SetSpanID(newSegmentID())
	span.SetParentSpanID(newSegmentID())
	span.SetName("operation")
	span.SetKind(tc.kind)
	span.SetStartTimestamp(pcommon.Timestamp(1_700_000_000_000_000_000))
	span.SetEndTimestamp(pcommon.Timestamp(1_700_000_001_000_000_000))
	constructSpanAttributes(attrs).CopyTo(span.Attributes())
	if len(eventAttrs) > 0 {
		event := span.Events().AppendEmpty()
		event.SetName("message")
		constructSpanAttributes(eventAttrs).CopyTo(event.Attributes())
	}
	segment, err := MakeSegment(span, pcommon.NewResource(), nil, false, nil, false)
	require.NoError(t, err)
	return segment
}

// setGates disables every migration gate, then applies overrides. Gates are reset on cleanup.
func setGates(t *testing.T, overrides map[*featuregate.Gate]bool) {
	t.Helper()
	for _, gate := range allMigrationGates {
		enabled := overrides[gate]
		prev := gate.IsEnabled()
		require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), enabled))
		t.Cleanup(func() { require.NoError(t, featuregate.GlobalRegistry().Set(gate.ID(), prev)) })
	}
}

func union(a, b map[string]any) map[string]any {
	if a == nil && b == nil {
		return nil
	}
	out := maps.Clone(a)
	if out == nil {
		out = map[string]any{}
	}
	maps.Copy(out, b)
	return out
}

func segmentDocument(t *testing.T, segment *awsxray.Segment) string {
	t.Helper()
	// IDs are random per span; blank them so documents are comparable.
	segment.ID, segment.TraceID, segment.ParentID = nil, nil, nil
	doc, err := MakeDocumentFromSegment(segment)
	require.NoError(t, err)
	return doc
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func defaultMetadata(s *awsxray.Segment) map[string]any {
	return s.Metadata["default"]
}

func segmentName(s *awsxray.Segment) string { return deref(s.Name) }

func httpRequest(s *awsxray.Segment, field func(*awsxray.RequestData) *string) *string {
	if s.HTTP == nil || s.HTTP.Request == nil {
		return nil
	}
	return field(s.HTTP.Request)
}

func httpURL(s *awsxray.Segment) string {
	return deref(httpRequest(s, func(r *awsxray.RequestData) *string { return r.URL }))
}

func httpStatus(s *awsxray.Segment) string {
	if s.HTTP == nil || s.HTTP.Response == nil || s.HTTP.Response.Status == nil {
		return ""
	}
	return strconv.FormatInt(*s.HTTP.Response.Status, 10)
}

func httpContentLength(s *awsxray.Segment) string {
	if s.HTTP == nil || s.HTTP.Response == nil || s.HTTP.Response.ContentLength == nil {
		return ""
	}
	switch v := s.HTTP.Response.ContentLength.(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case *int64:
		if v == nil {
			return ""
		}
		return strconv.FormatInt(*v, 10)
	default:
		return ""
	}
}

func sqlField(s *awsxray.Segment, field func(*awsxray.SQLData) *string) *string {
	if s.SQL == nil {
		return nil
	}
	return field(s.SQL)
}

func awsField(s *awsxray.Segment, field func(*awsxray.AWSData) *string) *string {
	if s.AWS == nil {
		return nil
	}
	return field(s.AWS)
}
