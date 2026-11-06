// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package translator // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/translator"

import (
	"maps"
	"net"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	conventions "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/awsxrayexporter/internal/metadata"
	awsxray "github.com/open-telemetry/opentelemetry-collector-contrib/internal/aws/xray"
)

func makeHTTP(span ptrace.Span) (map[string]pcommon.Value, *awsxray.HTTPData) {
	var (
		info = awsxray.HTTPData{
			Request:  &awsxray.RequestData{},
			Response: &awsxray.ResponseData{},
		}
		filtered = make(map[string]pcommon.Value)
		urlParts = make(map[string]string)
	)

	if span.Attributes().Len() == 0 {
		return filtered, nil
	}

	hasHTTP := false
	hasHTTPRequestURLAttributes := false
	hasNetPeerAddr := false
	emitV1 := metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled()

	for key, value := range span.Attributes().All() {
		switch key {
		case string("http.method"), string(conventions.HTTPRequestMethodKey):
			// TODO: Remove v0 key handling when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if key == string("http.method") && metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			if emitV1 && key == "http.method" {
				if _, ok := span.Attributes().Get(string(conventions.HTTPRequestMethodKey)); ok {
					break
				}
			}
			info.Request.Method = awsxray.String(value.Str())
			hasHTTP = true
		case string("http.client_ip"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			info.Request.ClientIP = awsxray.String(value.Str())
			hasHTTP = true
		case string("http.user_agent"), string(conventions.UserAgentOriginalKey):
			// TODO: Remove v0 key handling when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if key == string("http.user_agent") && metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			if emitV1 && key == "http.user_agent" {
				if _, ok := span.Attributes().Get(string(conventions.UserAgentOriginalKey)); ok {
					break
				}
			}
			info.Request.UserAgent = awsxray.String(value.Str())
			hasHTTP = true
		case string("http.status_code"), string(conventions.HTTPResponseStatusCodeKey):
			// TODO: Remove v0 key handling when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			// AWS SDK instrumentations still emit only http.status_code, so keep it for AWS SDK spans.
			if key == string("http.status_code") && metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() &&
				!isAwsSdkSpan(span) {
				filtered[key] = value
				break
			}
			if emitV1 && key == "http.status_code" {
				if _, ok := span.Attributes().Get(string(conventions.HTTPResponseStatusCodeKey)); ok {
					break
				}
			}
			info.Response.Status = aws.Int64(value.Int())
			hasHTTP = true
		case string("http.url"), string(conventions.URLFullKey):
			// TODO: Remove v0 key handling when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if key == string("http.url") && metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			if emitV1 {
				urlParts[key] = value.Str()
			} else {
				urlParts["http.url"] = value.Str()
			}
			hasHTTP = true
			hasHTTPRequestURLAttributes = true
		case string("http.scheme"), string(conventions.URLSchemeKey):
			// TODO: Remove v0 key handling when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if key == string("http.scheme") && metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			if emitV1 {
				urlParts[key] = value.Str()
			} else {
				urlParts["http.scheme"] = value.Str()
			}
			hasHTTP = true
		case string("http.host"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			hasHTTP = true
			hasHTTPRequestURLAttributes = true
		case string("http.target"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			hasHTTP = true
		case string("http.server_name"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			hasHTTP = true
			hasHTTPRequestURLAttributes = true
		case string("net.host.port"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			hasHTTP = true
			if urlParts[key] == "" {
				urlParts[key] = strconv.FormatInt(value.Int(), 10)
			}
		case string(conventions.HostNameKey):
			urlParts[key] = value.Str()
			hasHTTPRequestURLAttributes = true
		case string("net.host.name"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			hasHTTPRequestURLAttributes = true
		case string("net.peer.name"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			hasHTTPRequestURLAttributes = true
		case string("net.peer.port"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			urlParts[key] = value.Str()
			if urlParts[key] == "" {
				urlParts[key] = strconv.FormatInt(value.Int(), 10)
			}
		case string("net.peer.ip"):
			// TODO: Remove when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
			if metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				filtered[key] = value
				break
			}
			// Prefer HTTP forwarded information (AttributeHTTPClientIP) when present.
			if info.Request.ClientIP == nil {
				info.Request.ClientIP = awsxray.String(value.Str())
			}
			urlParts[key] = value.Str()
			hasHTTPRequestURLAttributes = true
			hasNetPeerAddr = true
		case string(conventions.NetworkPeerAddressKey):
			// Prefer HTTP forwarded information (AttributeHTTPClientIP) when present.
			if net.ParseIP(value.Str()) != nil {
				if info.Request.ClientIP == nil {
					info.Request.ClientIP = awsxray.String(value.Str())
				}
				hasHTTPRequestURLAttributes = true
				hasNetPeerAddr = true
				if emitV1 {
					urlParts[key] = value.Str()
				}
			} else if emitV1 {
				filtered[key] = value
			}
		case string(conventions.NetworkPeerPortKey):
			if metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() {
				urlParts[key] = value.Str()
				if urlParts[key] == "" {
					urlParts[key] = strconv.FormatInt(value.Int(), 10)
				}
			} else {
				filtered[key] = value
			}
		case string(conventions.ClientAddressKey):
			if net.ParseIP(value.Str()) != nil {
				info.Request.ClientIP = awsxray.String(value.Str())
			} else if emitV1 {
				filtered[key] = value
			}
		case string(conventions.URLPathKey):
			urlParts[key] = value.Str()
			hasHTTP = true
		case string(conventions.URLQueryKey):
			urlParts[key] = value.Str()
			hasHTTP = true
		case string(conventions.ServerAddressKey):
			urlParts[key] = value.Str()
			hasHTTPRequestURLAttributes = true
		case string(conventions.ServerPortKey):
			urlParts[key] = value.Str()
			if urlParts[key] == "" {
				urlParts[key] = strconv.FormatInt(value.Int(), 10)
			}
		default:
			filtered[key] = value
		}
	}

	if emitV1 {
		// Resolve after iteration so v1-over-v0 precedence does not depend on
		// attribute order. Leave the original translation unchanged by default.
		info.Request.ClientIP = nil
		keys := []string{string(conventions.ClientAddressKey), "http.client_ip", string(conventions.NetworkPeerAddressKey), "net.peer.ip"}
		if span.Kind() == ptrace.SpanKindServer {
			// Semconv v1.20 era server instrumentations (e.g. ADOT Java 1.x) report the peer only
			// as net.sock.peer.addr.
			keys = append(keys, "net.sock.peer.addr")
		}
		for _, key := range keys {
			legacy := key == "http.client_ip" || key == "net.peer.ip" || key == "net.sock.peer.addr"
			if legacy && metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
				continue
			}
			if value, ok := span.Attributes().Get(key); ok && net.ParseIP(value.Str()) != nil {
				info.Request.ClientIP = awsxray.String(value.Str())
				if key == "net.sock.peer.addr" {
					hasNetPeerAddr = true // the address of the direct peer, not a forwarded one
				}
				break
			}
		}
	}

	if !hasNetPeerAddr && info.Request.ClientIP != nil {
		info.Request.XForwardedFor = aws.Bool(true)
	}

	if !hasHTTP {
		// Didn't have any HTTP-specific information so don't need to fill it in segment
		// Shared network attributes still belong to DB and RPC spans. They are owned by the
		// HTTP/network gate so that the DB and RPC gates do not change non-DB/RPC output.
		if emitV1 {
			maps.Insert(filtered, span.Attributes().All())
		}
		return filtered, nil
	}

	if hasHTTPRequestURLAttributes {
		if span.Kind() == ptrace.SpanKindServer {
			info.Request.URL = awsxray.String(constructServerURL(urlParts))
		} else {
			info.Request.URL = awsxray.String(constructClientURL(urlParts))
		}
	}

	info.Response.ContentLength = aws.Int64(extractResponseSizeFromEvents(span))

	return filtered, &info
}

func extractResponseSizeFromEvents(span ptrace.Span) int64 {
	// Support instrumentation that sets response size in span or as an event.
	size := extractResponseSizeFromAttributes(span.Attributes())
	if size != 0 {
		return size
	}
	for i := 0; i < span.Events().Len(); i++ {
		event := span.Events().At(i)
		size = extractResponseSizeFromAttributes(event.Attributes())
		if size != 0 {
			return size
		}
	}
	return size
}

func extractResponseSizeFromAttributes(attributes pcommon.Map) int64 {
	typeVal, ok := attributes.Get("message.type")
	if ok && typeVal.Str() == "RECEIVED" {
		if metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() {
			if sizeVal, ok := attributes.Get(string(conventions.MessagingMessageBodySizeKey)); ok {
				return sizeVal.Int()
			}
		}
		// TODO: Remove old key when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
		if !metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
			if sizeVal, ok := attributes.Get(string("messaging.message_payload_size_bytes")); ok {
				return sizeVal.Int()
			}
		}
	}
	return 0
}

func constructClientURL(urlParts map[string]string) string {
	// follows OpenTelemetry specification-defined combinations for client spans described in
	// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/http/http-spans.md#http-client

	url, ok := httpURLPart(urlParts, string(conventions.URLFullKey), "http.url")
	if ok {
		// full URL available so no need to assemble
		return url
	}

	scheme, hasScheme := httpURLPart(urlParts, string(conventions.URLSchemeKey), "http.scheme")
	if !hasScheme {
		scheme = "http"
	}
	port := ""
	// server.address/server.port replace net.peer.name/net.peer.port for client spans:
	// https://opentelemetry.io/docs/specs/semconv/non-normative/http-migration/#http-client-span-attributes
	emitV1 := metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled()
	v1Host, hasV1Host := urlParts[string(conventions.ServerAddressKey)]
	hasV1Host = hasV1Host && v1Host != ""
	// TODO: Remove old-key lookups when exporter.awsxray.DontEmitV0HTTPNetworkConventions is removed.
	host, ok := urlParts[string("http.host")]
	if !ok || (emitV1 && (hasV1Host || host == "")) {
		if emitV1 && hasV1Host {
			host = v1Host
		} else {
			host, ok = urlParts[string("net.peer.name")]
			if !ok {
				host, ok = urlParts[string("net.peer.ip")]
				if !ok {
					host = v1Host
					if emitV1 && host == "" {
						host = urlParts[string(conventions.NetworkPeerAddressKey)]
					}
				}
			}
		}
		port, ok = urlParts[string(conventions.ServerPortKey)]
		if !ok || !emitV1 {
			port, ok = urlParts["net.peer.port"]
			if !ok {
				port = urlParts[string(conventions.NetworkPeerPortKey)]
			}
		}
	}
	scheme = inferHTTPScheme(scheme, hasScheme, port)
	url = scheme + "://" + constructHTTPHostPort(host, port, scheme)
	return appendHTTPTarget(url, urlParts)
}

func constructServerURL(urlParts map[string]string) string {
	// follows OpenTelemetry specification-defined combinations for server spans described in
	// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/http/http-spans.md#http-server

	url, ok := httpURLPart(urlParts, string(conventions.URLFullKey), "http.url")
	if ok {
		// full URL available so no need to assemble
		return url
	}

	scheme, hasScheme := httpURLPart(urlParts, string(conventions.URLSchemeKey), "http.scheme")
	if !hasScheme {
		scheme = "http"
	}
	port := ""
	if metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() {
		if host, hasServerAddress := urlParts[string(conventions.ServerAddressKey)]; hasServerAddress && host != "" {
			port, _ = httpURLPart(urlParts, string(conventions.ServerPortKey), "net.host.port")
			scheme = inferHTTPScheme(scheme, hasScheme, port)
			url = scheme + "://" + constructHTTPHostPort(host, port, scheme)
			return appendHTTPTarget(url, urlParts)
		}
	}
	host, ok := urlParts[string("http.host")]
	if !ok {
		host, ok = urlParts[string("http.server_name")]
		if !ok {
			host, ok = urlParts[string("net.host.name")]
			if !ok {
				host, ok = urlParts[string(conventions.HostNameKey)]
				if !ok {
					host = urlParts[string(conventions.ServerAddressKey)]
				}
			}
		}
		port, ok = urlParts[string("net.host.port")]
		if !ok {
			port, ok = urlParts[string(conventions.ServerPortKey)]
			if !ok {
				port = ""
			}
		}
	}
	url = scheme + "://" + constructHTTPHostPort(host, port, scheme)
	return appendHTTPTarget(url, urlParts)
}

func constructHTTPHostPort(host, port, scheme string) string {
	if !metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() {
		if port != "" && (scheme != "http" || port != "80") && (scheme != "https" || port != "443") {
			return host + ":" + port
		}
		return host
	}
	bare := strings.Trim(host, "[]")
	isIPv6 := strings.Contains(bare, ":") && net.ParseIP(bare) != nil
	if !isIPv6 && strings.Contains(host, ":") {
		// Some instrumentations put the Host header (host:port) into server.address;
		// it already carries the port, so do not append or bracket it.
		return host
	}
	if isIPv6 {
		host = "[" + bare + "]"
	}
	if port != "" && (scheme != "http" || port != "80") && (scheme != "https" || port != "443") {
		return host + ":" + port
	}
	return host
}

// inferHTTPScheme returns https for port 443 when no scheme attribute was provided
// (e.g. AWS SDK spans that only carry server.address/server.port). Gated by EmitV1HTTP.
func inferHTTPScheme(scheme string, hasScheme bool, port string) string {
	if !hasScheme && port == "443" && metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() {
		return "https"
	}
	return scheme
}

func httpURLPart(parts map[string]string, v1Key, v0Key string) (string, bool) {
	if metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() {
		if value, ok := parts[v1Key]; ok {
			return value, true
		}
	}
	if !metadata.ExporterAwsxrayDontEmitV0HTTPNetworkConventionsFeatureGate.IsEnabled() {
		if value, ok := parts[v0Key]; ok {
			return value, true
		}
	}
	value, ok := parts[v1Key]
	return value, ok
}

func appendHTTPTarget(url string, parts map[string]string) string {
	path, hasPath := parts[string(conventions.URLPathKey)]
	query, hasQuery := parts[string(conventions.URLQueryKey)]
	if target, ok := parts["http.target"]; ok &&
		(!metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled() || !hasPath) {
		return url + target
	}
	if hasPath {
		url += path
	} else {
		url += "/"
	}
	if hasQuery && (query != "" || !metadata.ExporterAwsxrayEmitV1HTTPNetworkConventionsFeatureGate.IsEnabled()) {
		if !strings.HasPrefix(query, "?") {
			query = "?" + query
		}
		url += query
	}
	return url
}
