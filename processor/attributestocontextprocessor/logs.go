// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor"

import (
	"context"
	"errors"
	"maps"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"
)

type logsProcessor struct {
	actions actions.Actions
	next    consumer.Logs
}

func newLogsProcessor(cfg *Config, next consumer.Logs) processor.Logs {
	return &logsProcessor{
		actions: actions.NewActions(cfg.Actions),
		next:    next,
	}
}

func (p *logsProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	resourceLogs := ld.ResourceLogs()
	if resourceLogs.Len() == 0 {
		return p.next.ConsumeLogs(ctx, ld)
	}

	clientInfo := client.FromContext(ctx)
	baseMetadata := make(map[string][]string)
	for key := range clientInfo.Metadata.Keys() {
		baseMetadata[key] = clientInfo.Metadata.Get(key)
	}

	// Fast path: single ResourceLogs, no grouping needed.
	if resourceLogs.Len() == 1 {
		p.actions.ProcessResource(baseMetadata, resourceLogs.At(0).Resource().Attributes())
		clientInfo.Metadata = client.NewMetadata(baseMetadata)
		return p.next.ConsumeLogs(client.NewContext(ctx, clientInfo), ld)
	}

	type group struct {
		metadata map[string][]string
		indices  []int
	}
	groups := map[string]*group{}

	for i := 0; i < resourceLogs.Len(); i++ {
		metadataMap := maps.Clone(baseMetadata)
		p.actions.ProcessResource(metadataMap, resourceLogs.At(i).Resource().Attributes())

		groupKey := p.actions.GroupKey(metadataMap)
		g, ok := groups[groupKey]
		if !ok {
			g = &group{metadata: metadataMap}
			groups[groupKey] = g
		}
		g.indices = append(g.indices, i)
	}

	// All ResourceLogs share the same metadata, pass original data without copying.
	if len(groups) == 1 {
		for _, g := range groups {
			clientInfo.Metadata = client.NewMetadata(g.metadata)
			return p.next.ConsumeLogs(client.NewContext(ctx, clientInfo), ld)
		}
	}

	var errs []error
	for _, g := range groups {
		clientInfo.Metadata = client.NewMetadata(g.metadata)
		newCtx := client.NewContext(ctx, clientInfo)
		logs := plog.NewLogs()
		for _, idx := range g.indices {
			resourceLogs.At(idx).CopyTo(logs.ResourceLogs().AppendEmpty())
		}
		if err := p.next.ConsumeLogs(newCtx, logs); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (*logsProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (*logsProcessor) Start(context.Context, component.Host) error {
	return nil
}

func (*logsProcessor) Shutdown(context.Context) error {
	return nil
}
