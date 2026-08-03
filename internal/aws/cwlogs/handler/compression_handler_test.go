// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runRequestCompressionStack builds a smithy middleware stack, registers the
// middleware appended by WithRequestCompression via the cloudwatchlogs client
// options, sends payload through it, and returns the captured request headers
// and body as seen by the terminal handler.
func runRequestCompressionStack(t *testing.T, disable bool, minSize int64, payload []byte) (http.Header, []byte) {
	t.Helper()

	stack := middleware.NewStack("request compression", smithyhttp.NewStackRequest)

	// Seed the request stream before the compression middleware (registered
	// with middleware.After) runs.
	setPayload := middleware.SerializeMiddlewareFunc("SetPayload",
		func(ctx context.Context, in middleware.SerializeInput, next middleware.SerializeHandler) (middleware.SerializeOutput, middleware.Metadata, error) {
			req, ok := in.Request.(*smithyhttp.Request)
			require.True(t, ok)
			newReq, err := req.SetStream(bytes.NewReader(payload))
			require.NoError(t, err)
			in.Request = newReq
			return next.HandleSerialize(ctx, in)
		})
	require.NoError(t, stack.Serialize.Add(setPayload, middleware.Before))

	opts := cloudwatchlogs.Options{}
	WithRequestCompression(disable, minSize)(&opts)
	require.Len(t, opts.APIOptions, 1)
	require.NoError(t, opts.APIOptions[0](stack))

	var gotHeader http.Header
	var gotBody []byte
	mockHandler := middleware.HandlerFunc(func(ctx context.Context, in any) (any, middleware.Metadata, error) {
		req := in.(*smithyhttp.Request).Build(ctx)
		gotHeader = req.Header
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		gotBody = body
		return &smithyhttp.Response{
			Response: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
			},
		}, middleware.Metadata{}, nil
	})

	handler := middleware.DecorateHandler(mockHandler, stack)
	_, _, err := handler.Handle(t.Context(), nil)
	require.NoError(t, err)
	return gotHeader, gotBody
}

func TestWithRequestCompressionAboveMinSize(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 10240)
	header, body := runRequestCompressionStack(t, false, 10240, payload)

	assert.Equal(t, "gzip", header.Get("Content-Encoding"))
	require.NotEqual(t, payload, body)

	gz, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	decompressed, err := io.ReadAll(gz)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	assert.Equal(t, payload, decompressed)
}

func TestWithRequestCompressionBelowMinSize(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 10239)
	header, body := runRequestCompressionStack(t, false, 10240, payload)

	assert.Empty(t, header.Get("Content-Encoding"))
	assert.Equal(t, payload, body)
}

func TestWithRequestCompressionDisabled(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 10240)
	header, body := runRequestCompressionStack(t, true, 10240, payload)

	assert.Empty(t, header.Get("Content-Encoding"))
	assert.Equal(t, payload, body)
}
