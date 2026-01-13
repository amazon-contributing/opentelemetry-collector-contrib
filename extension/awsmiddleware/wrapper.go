// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsmiddleware // import "github.com/amazon-contributing/opentelemetry-collector-contrib/extension/awsmiddleware"

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go/aws/request"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
)

type (
	requestIDKey     struct{}
	operationNameKey struct{}
)

func namedRequestHandler(handler RequestHandler) request.NamedHandler {
	return request.NamedHandler{Name: handler.ID(), Fn: func(r *request.Request) {
		ctx := mustRequestID(r.Context())
		ctx = setOperationName(ctx, r.Operation.Name)
		r.SetContext(ctx)
		handler.HandleRequest(ctx, r.HTTPRequest)
	}}
}

func namedResponseHandler(handler ResponseHandler) request.NamedHandler {
	return request.NamedHandler{Name: handler.ID(), Fn: func(r *request.Request) {
		handler.HandleResponse(r.Context(), r.HTTPResponse)
	}}
}

type requestMiddleware struct {
	RequestHandler
}

var _ smithymiddleware.BuildMiddleware = (*requestMiddleware)(nil)

func (r requestMiddleware) HandleBuild(ctx context.Context, in smithymiddleware.BuildInput, next smithymiddleware.BuildHandler) (out smithymiddleware.BuildOutput, metadata smithymiddleware.Metadata, err error) {
	req, ok := in.Request.(*smithyhttp.Request)
	if ok {
		ctx = mustRequestID(ctx)
		ctx = setOperationName(ctx, middleware.GetOperationName(ctx))
		r.HandleRequest(ctx, req.Request)
	}
	return next.HandleBuild(ctx, in)
}

func withBuildOption(rmw *requestMiddleware, position smithymiddleware.RelativePosition) func(stack *smithymiddleware.Stack) error {
	return func(stack *smithymiddleware.Stack) error {
		return stack.Build.Add(rmw, position)
	}
}

type responseMiddleware struct {
	ResponseHandler
}

var _ smithymiddleware.DeserializeMiddleware = (*responseMiddleware)(nil)

func (r responseMiddleware) HandleDeserialize(ctx context.Context, in smithymiddleware.DeserializeInput, next smithymiddleware.DeserializeHandler) (out smithymiddleware.DeserializeOutput, metadata smithymiddleware.Metadata, err error) {
	out, metadata, err = next.HandleDeserialize(ctx, in)
	res, ok := out.RawResponse.(*smithyhttp.Response)
	if ok {
		r.HandleResponse(ctx, res.Response)
	}
	return
}

func withDeserializeOption(rmw *responseMiddleware, position smithymiddleware.RelativePosition) func(stack *smithymiddleware.Stack) error {
	return func(stack *smithymiddleware.Stack) error {
		return stack.Deserialize.Add(rmw, position)
	}
}

func mustRequestID(ctx context.Context) context.Context {
	requestID := GetRequestID(ctx)
	if requestID != "" {
		return ctx
	}
	return setRequestID(ctx, uuid.NewString())
}

func setRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

func setOperationName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, operationNameKey{}, name)
}

// GetRequestID retrieves the generated request ID from the context.
func GetRequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}

// GetOperationName retrieves the service operation metadata from the context.
func GetOperationName(ctx context.Context) string {
	operationName, _ := ctx.Value(operationNameKey{}).(string)
	return operationName
}
