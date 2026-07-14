// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awscloudwatchlogsprovisionerextension

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/mock"
	"go.opentelemetry.io/collector/component"
)

type mockCWLogsClient struct {
	mock.Mock
}

func (m *mockCWLogsClient) CreateLogGroup(_ context.Context, logGroupName string, logGroupClass types.LogGroupClass) error {
	args := m.Called(logGroupName, logGroupClass)
	return args.Error(0)
}

func (m *mockCWLogsClient) PutRetentionPolicy(_ context.Context, logGroupName string, retentionInDays int32) error {
	args := m.Called(logGroupName, retentionInDays)
	return args.Error(0)
}

func (m *mockCWLogsClient) DescribeLogGroupsRetention(_ context.Context, logGroupNames []string) (map[string]int32, error) {
	args := m.Called(logGroupNames)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]int32), args.Error(1)
}

func (m *mockCWLogsClient) CreateLogStream(_ context.Context, logGroupName, logStreamName string) error {
	args := m.Called(logGroupName, logStreamName)
	return args.Error(0)
}

type mockHTTPClient struct {
	component.StartFunc
	component.ShutdownFunc
}

func (m *mockHTTPClient) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	return base, nil
}

type mockAuthWithHeader struct {
	component.StartFunc
	component.ShutdownFunc
	headerKey   string
	headerValue string
}

func (m *mockAuthWithHeader) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		req2 := req.Clone(req.Context())
		req2.Header.Set(m.headerKey, m.headerValue)
		return base.RoundTrip(req2)
	}), nil
}

type mockHost struct {
	extensions map[component.ID]component.Component
}

func (h *mockHost) GetExtensions() map[component.ID]component.Component {
	return h.extensions
}

// newStubbedCWLogsClient returns a defaultCWLogsClient whose SDK client sends
// requests to the given RoundTripper instead of the network. The SDK's real
// serialization and error deserialization still run.
func newStubbedCWLogsClient(rt roundTripperFunc) *defaultCWLogsClient {
	svc := cloudwatchlogs.New(cloudwatchlogs.Options{
		Region:           "us-east-1",
		Credentials:      aws.AnonymousCredentials{},
		HTTPClient:       &http.Client{Transport: rt},
		RetryMaxAttempts: 1,
	})
	return &defaultCWLogsClient{svc: svc}
}

// awsJSONError builds an AWS JSON protocol error response.
func awsJSONError(errType, message string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header: http.Header{
			"Content-Type":     []string{"application/x-amz-json-1.1"},
			"X-Amzn-Errortype": []string{errType},
		},
		Body: io.NopCloser(strings.NewReader(`{"__type":"` + errType + `","message":"` + message + `"}`)),
	}
}

func filterCalls(calls []mock.Call, method string) []mock.Call {
	var result []mock.Call
	for _, c := range calls {
		if c.Method == method {
			result = append(result, c)
		}
	}
	return result
}
