// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor"

import (
	"context"

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
	clientInfo := client.FromContext(ctx)
	metadataMap := make(map[string][]string)
	for key := range clientInfo.Metadata.Keys() {
		metadataMap[key] = clientInfo.Metadata.Get(key)
	}

	resourceLogs := ld.ResourceLogs()
	for i := 0; i < resourceLogs.Len(); i++ {
		p.actions.ProcessResource(metadataMap, resourceLogs.At(i).Resource().Attributes())
	}

	clientInfo.Metadata = client.NewMetadata(metadataMap)
	newCtx := client.NewContext(ctx, clientInfo)
	return p.next.ConsumeLogs(newCtx, ld)
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
