// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package appsignals

import (
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/stretchr/testify/assert"
	semconv "go.opentelemetry.io/collector/semconv/v1.6.1"
)

func TestUserAgent(t *testing.T) {
	testCases := map[string]struct {
		labelSets []map[string]string
		want      []string
	}{
		"WithEmpty": {},
		"WithMultipleLanguages": {
			labelSets: []map[string]string{
				{
					semconv.AttributeTelemetrySDKLanguage: "foo",
					semconv.AttributeTelemetrySDKVersion:  "1.1",
				},
				{
					semconv.AttributeTelemetrySDKLanguage: "bar",
					semconv.AttributeTelemetrySDKVersion:  "1.0",
				},
			},
			want: []string{
				"telemetry-sdk-bar/1.0",
				"telemetry-sdk-foo/1.1",
			},
		},
		"WithMultipleVersions": {
			labelSets: []map[string]string{
				{
					semconv.AttributeTelemetrySDKLanguage: "test",
					semconv.AttributeTelemetrySDKVersion:  "1.1",
				},
				{
					semconv.AttributeTelemetrySDKLanguage: "test",
					semconv.AttributeTelemetrySDKVersion:  "1.0",
				},
			},
			want: []string{
				"telemetry-sdk-test/1.0",
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			userAgent := NewUserAgent()
			for _, labelSet := range testCase.labelSets {
				userAgent.Process(labelSet)
			}
			req := &request.Request{
				HTTPRequest: &http.Request{
					Header: http.Header{},
				},
			}
			userAgent.Handler().Fn(req)
			got := req.HTTPRequest.Header.Get("User-Agent")
			if got == "" {
				assert.Empty(t, testCase.want)
			} else {
				gotUserAgents := strings.Split(got, " ")
				sort.Strings(gotUserAgents)
				assert.Equal(t, testCase.want, gotUserAgents)
			}
		})
	}
}

func TestUserAgentExpiration(t *testing.T) {
	userAgent := newUserAgent(50 * time.Millisecond)
	req := &request.Request{
		HTTPRequest: &http.Request{
			Header: http.Header{},
		},
	}
	labels := map[string]string{
		semconv.AttributeTelemetrySDKLanguage: "test",
		semconv.AttributeTelemetrySDKVersion:  "1.0",
	}
	userAgent.Process(labels)
	userAgent.handle(req)
	got := req.HTTPRequest.Header.Get("User-Agent")
	assert.Equal(t, "telemetry-sdk-test/1.0", got)

	// wait for expiration
	time.Sleep(100 * time.Millisecond)
	// reset user-agent header
	req.HTTPRequest.Header.Del("User-Agent")
	userAgent.handle(req)
	got = req.HTTPRequest.Header.Get("User-Agent")
	assert.Empty(t, got)
}
