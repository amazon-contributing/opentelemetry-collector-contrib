// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsmiddleware

import (
	"encoding"
	"errors"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/smithy-go/middleware"
	"go.opentelemetry.io/collector/extension"
)

var (
	errNotFound            = errors.New("middleware not found")
	errNotMiddleware       = errors.New("extension is not an AWS middleware")
	errInvalidHandler      = errors.New("invalid handler")
	errUnsupportedPosition = errors.New("unsupported position")
)

// HandlerPosition is the relative position of a handler used during insertion.
type HandlerPosition int

var _ encoding.TextMarshaler = (*HandlerPosition)(nil)
var _ encoding.TextUnmarshaler = (*HandlerPosition)(nil)

const (
	After HandlerPosition = iota
	Before

	afterStr  = "after"
	beforeStr = "before"
)

// String returns an empty string if unsupported position.
func (h HandlerPosition) String() string {
	switch h {
	case Before:
		return beforeStr
	case After:
		return afterStr
	default:
		return ""
	}
}

// MarshalText converts the position into a string. Returns an error
// if unsupported.
func (h HandlerPosition) MarshalText() (text []byte, err error) {
	s := h.String()
	if s == "" {
		return nil, fmt.Errorf("%w: %[2]T(%[2]d)", errUnsupportedPosition, h)
	}
	return []byte(h.String()), nil
}

// UnmarshalText converts the string into a position. Returns an error
// if unsupported.
func (h *HandlerPosition) UnmarshalText(text []byte) error {
	switch s := string(text); s {
	case afterStr:
		*h = After
	case beforeStr:
		*h = Before
	default:
		return fmt.Errorf("%w: %s", errUnsupportedPosition, s)
	}
	return nil
}

// HandlerMetadata is used to differentiate between handlers and determine
// relative insert positioning within their groups.
type HandlerMetadata interface {
	// ID is used to identify the handler.
	ID() string
	// Position to insert the handler.
	Position() HandlerPosition
}

// RequestHandler allows for custom processing of requests.
type RequestHandler interface {
	HandlerMetadata
	HandleRequest(r *http.Request)
}

// ResponseHandler allows for custom processing of responses.
type ResponseHandler interface {
	HandlerMetadata
	HandleResponse(r *http.Response)
}

// Middleware is the request and response handlers to be configured
// on AWS Clients.
type Middleware interface {
	RequestHandlers() []RequestHandler
	ResponseHandlers() []ResponseHandler
}

// MiddlewareExtension is an extension that implements Middleware.
type MiddlewareExtension interface {
	extension.Extension
	Middleware
}

// ConfigureSDKv1 adds middleware to the AWS SDK v1. Request handlers are added to the
// Build handler list and response handlers are added to the Unmarshal handler list.
func ConfigureSDKv1(mw Middleware, handlers *request.Handlers) error {
	var errs error
	for _, handler := range mw.RequestHandlers() {
		if err := addHandlerToList(&handlers.Build, namedRequestHandler(handler), handler.Position()); err != nil {
			errs = errors.Join(errs, fmt.Errorf("%w (%q): %w", errInvalidHandler, handler.ID(), err))
		}
	}
	for _, handler := range mw.ResponseHandlers() {
		if err := addHandlerToList(&handlers.Unmarshal, namedResponseHandler(handler), handler.Position()); err != nil {
			errs = errors.Join(errs, fmt.Errorf("%w (%q): %w", errInvalidHandler, handler.ID(), err))
		}
	}
	return errs
}

// ConfigureSDKv2 adds middleware to the AWS SDK v2. Request handlers are added to the
// Build step and response handlers are added to the Deserialize step.
func ConfigureSDKv2(mw Middleware, config *aws.Config) error {
	var errs error
	for _, handler := range mw.RequestHandlers() {
		relativePosition, err := toRelativePosition(handler.Position())
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("%w (%q): %w", errInvalidHandler, handler.ID(), err))
			continue
		}
		rmw := &requestMiddleware{RequestHandler: handler, position: relativePosition}
		config.APIOptions = append(config.APIOptions, func(stack *middleware.Stack) error {
			return stack.Build.Add(rmw, rmw.position)
		})
	}
	for _, handler := range mw.ResponseHandlers() {
		relativePosition, err := toRelativePosition(handler.Position())
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("%w (%q): %w", errInvalidHandler, handler.ID(), err))
			continue
		}
		rmw := &responseMiddleware{ResponseHandler: handler, position: relativePosition}
		config.APIOptions = append(config.APIOptions, func(stack *middleware.Stack) error {
			return stack.Deserialize.Add(rmw, rmw.position)
		})
	}
	return errs
}

// addHandlerToList adds the handler to the list based on the position.
func addHandlerToList(handlerList *request.HandlerList, handler request.NamedHandler, position HandlerPosition) error {
	relativePosition, err := toRelativePosition(position)
	if err != nil {
		return err
	}
	switch relativePosition {
	case middleware.Before:
		handlerList.PushFrontNamed(handler)
	case middleware.After:
		handlerList.PushBackNamed(handler)
	}
	return nil
}

// toRelativePosition maps the HandlerPosition to a middleware.RelativePosition. It also validates that
// the HandlerPosition provided is supported and returns an errUnsupportedPosition if it isn't.
func toRelativePosition(position HandlerPosition) (middleware.RelativePosition, error) {
	switch position {
	case Before:
		return middleware.Before, nil
	case After:
		return middleware.After, nil
	default:
		return -1, fmt.Errorf("%w: %s", errUnsupportedPosition, position)
	}
}
