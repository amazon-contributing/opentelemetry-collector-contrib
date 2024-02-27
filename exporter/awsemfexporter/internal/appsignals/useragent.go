// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package appsignals

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/jellydator/ttlcache/v3"
	semconv "go.opentelemetry.io/collector/semconv/v1.6.1"
)

const (
	handlerName = "aws.appsignals.UserAgentHandler"
)

type UserAgent struct {
	mu       sync.RWMutex
	prebuilt []string
	cache    *ttlcache.Cache[string, string]
}

func NewUserAgent() *UserAgent {
	return newUserAgent(time.Minute)
}

func newUserAgent(ttl time.Duration) *UserAgent {
	ua := &UserAgent{cache: ttlcache.New[string, string](ttlcache.WithTTL[string, string](ttl))}
	ua.cache.OnEviction(func(context.Context, ttlcache.EvictionReason, *ttlcache.Item[string, string]) {
		ua.rebuild()
	})
	go ua.cache.Start()
	return ua
}

// Handler creates a named handler with the UserAgent's handle function.
func (h *UserAgent) Handler() request.NamedHandler {
	return request.NamedHandler{
		Name: handlerName,
		Fn:   h.handle,
	}
}

// handle adds the pre-built user agent strings to the user agent header.
func (h *UserAgent) handle(r *request.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, str := range h.prebuilt {
		request.AddToUserAgent(r, str)
	}
}

// formatStr formats the telemetry SDK language and version into the user agent format.
func formatStr(language, version string) string {
	return fmt.Sprintf("telemetry-sdk-%s/%s", language, version)
}

// Process takes the telemetry SDK language and version and adds them to the cache. If it already exists in the cache
// and has the same value, extends the TTL. If not, then it sets it and rebuilds the user agent strings.
func (h *UserAgent) Process(labels map[string]string) {
	language := labels[semconv.AttributeTelemetrySDKLanguage]
	version := labels[semconv.AttributeTelemetrySDKVersion]
	if language != "" && version != "" {
		value := h.cache.Get(language)
		if value == nil || value.Value() != version {
			h.cache.Set(language, version, ttlcache.DefaultTTL)
			h.rebuild()
		}
	}
}

func (h *UserAgent) rebuild() {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := h.cache.Items()
	h.prebuilt = make([]string, 0, len(items))
	for _, item := range h.cache.Items() {
		h.prebuilt = append(h.prebuilt, formatStr(item.Key(), item.Value()))
	}
}
