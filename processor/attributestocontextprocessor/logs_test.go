// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/attributestocontextprocessor/internal/actions"
)

func TestLogsProcessor(t *testing.T) {
	cfg := &Config{
		Actions: []actions.KeyValue{
			{Key: "cwlogs.log_group", FromResourceAttribute: "cwlogs.log_group"},
			{Key: "cwlogs.log_stream", FromResourceAttribute: "cwlogs.log_stream"},
		},
	}

	var capturedCtx context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			capturedCtx = ctx
			return nil
		},
	}
	processor := newLogsProcessor(cfg, next)

	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("cwlogs.log_group", "/aws/telemetry/my-service")
	rl.Resource().Attributes().PutStr("cwlogs.log_stream", "default")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	assert.False(t, processor.Capabilities().MutatesData)

	clientInfo := client.FromContext(capturedCtx)
	assert.Equal(t, []string{"/aws/telemetry/my-service"}, clientInfo.Metadata.Get("cwlogs.log_group"))
	assert.Equal(t, []string{"default"}, clientInfo.Metadata.Get("cwlogs.log_stream"))
}

type mockLogsConsumer struct {
	consumeFunc func(ctx context.Context, ld plog.Logs) error
}

func (m *mockLogsConsumer) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	return m.consumeFunc(ctx, ld)
}

func (*mockLogsConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{}
}

func TestLogsProcessor_MultipleResourceLogs_LastWins(t *testing.T) {
	cfg := &Config{
		Actions: []actions.KeyValue{
			{Key: "cwlogs.log_group", FromResourceAttribute: "cwlogs.log_group"},
		},
	}

	var capturedCtx context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			capturedCtx = ctx
			return nil
		},
	}
	processor := newLogsProcessor(cfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/first")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/second")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	clientInfo := client.FromContext(capturedCtx)
	assert.Equal(t, []string{"/second"}, clientInfo.Metadata.Get("cwlogs.log_group"))
}

func TestLogsProcessor_PreservesUpstreamMetadata(t *testing.T) {
	cfg := &Config{
		Actions: []actions.KeyValue{
			{Key: "cwlogs.log_group", FromResourceAttribute: "cwlogs.log_group"},
		},
	}

	var capturedCtx context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			capturedCtx = ctx
			return nil
		},
	}
	processor := newLogsProcessor(cfg, next)

	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("cwlogs.log_group", "/my/group")

	upstream := client.NewMetadata(map[string][]string{
		"existing-key": {"existing-value"},
	})
	ctx := client.NewContext(t.Context(), client.Info{Metadata: upstream})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	clientInfo := client.FromContext(capturedCtx)
	assert.Equal(t, []string{"/my/group"}, clientInfo.Metadata.Get("cwlogs.log_group"))
	assert.Equal(t, []string{"existing-value"}, clientInfo.Metadata.Get("existing-key"))
}

func TestLogsProcessor_MissingAttribute_SkipsSilently(t *testing.T) {
	cfg := &Config{
		Actions: []actions.KeyValue{
			{Key: "cwlogs.log_group", FromResourceAttribute: "cwlogs.log_group"},
		},
	}

	var capturedCtx context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			capturedCtx = ctx
			return nil
		},
	}
	processor := newLogsProcessor(cfg, next)

	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty() // no attributes

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	clientInfo := client.FromContext(capturedCtx)
	assert.Nil(t, clientInfo.Metadata.Get("cwlogs.log_group"))
}

func TestLogsProcessorStart(t *testing.T) {
	processor := &logsProcessor{}
	err := processor.Start(t.Context(), nil)
	assert.NoError(t, err)
}

func TestLogsProcessorShutdown(t *testing.T) {
	processor := &logsProcessor{}
	err := processor.Shutdown(t.Context())
	assert.NoError(t, err)
}
