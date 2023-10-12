// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package awsmiddleware

import (
	"fmt"

	"go.opentelemetry.io/collector/component"
)

type Config struct {
	// MiddlewareID is the extension to use to configure the Middleware.
	MiddlewareID component.ID `mapstructure:"middleware"`
}

// GetMiddleware retrieves the extension implementing Middleware based on the
// MiddlewareID.
func (c Config) GetMiddleware(extensions map[component.ID]component.Component) (Middleware, error) {
	if ext, found := extensions[c.MiddlewareID]; found {
		if mw, ok := ext.(Middleware); ok {
			return mw, nil
		}
		return nil, errNotMiddleware
	}
	return nil, fmt.Errorf("failed to resolve AWS client handler %q: %w", c.MiddlewareID, errNotFound)
}
