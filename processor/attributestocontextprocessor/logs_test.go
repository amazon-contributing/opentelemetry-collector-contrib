// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package attributestocontextprocessor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
)

var singleKeyCfg = &Config{
	Actions: []ActionKeyValue{
		{Key: "cwlogs.log_group", FromResourceAttribute: "cwlogs.log_group"},
	},
}

func TestLogsProcessor(t *testing.T) {
	cfg := &Config{
		Actions: []ActionKeyValue{
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

func TestLogsProcessor_MultipleResourceLogs_PerResourceContext(t *testing.T) {
	var calls []context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			calls = append(calls, ctx)
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/first")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/second")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	assert.Len(t, calls, 2)
	groups := map[string]bool{}
	for _, c := range calls {
		groups[client.FromContext(c).Metadata.Get("cwlogs.log_group")[0]] = true
	}
	assert.True(t, groups["/first"])
	assert.True(t, groups["/second"])
}

func TestLogsProcessor_GroupsByMetadata(t *testing.T) {
	var callLogs []plog.Logs
	next := &mockLogsConsumer{
		consumeFunc: func(_ context.Context, ld plog.Logs) error {
			callLogs = append(callLogs, ld)
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/app")
	rl1.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("log1")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/platform")
	rl2.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("log2")
	rl3 := logs.ResourceLogs().AppendEmpty()
	rl3.Resource().Attributes().PutStr("cwlogs.log_group", "/app")
	rl3.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("log3")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	assert.Len(t, callLogs, 2)
	// One group has 2 ResourceLogs, the other has 1
	counts := map[int]int{}
	for _, ld := range callLogs {
		counts[ld.ResourceLogs().Len()]++
	}
	assert.Equal(t, 1, counts[2]) // /app group
	assert.Equal(t, 1, counts[1]) // /platform group
}

func TestLogsProcessor_PreservesExistingMetadata(t *testing.T) {
	var capturedCtx context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			capturedCtx = ctx
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("cwlogs.log_group", "/my/group")

	existing := client.NewMetadata(map[string][]string{
		"existing-key": {"existing-value"},
	})
	ctx := client.NewContext(t.Context(), client.Info{Metadata: existing})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	clientInfo := client.FromContext(capturedCtx)
	assert.Equal(t, []string{"/my/group"}, clientInfo.Metadata.Get("cwlogs.log_group"))
	assert.Equal(t, []string{"existing-value"}, clientInfo.Metadata.Get("existing-key"))
}

func TestLogsProcessor_PreservesExistingMetadata_MultiGroup(t *testing.T) {
	var calls []context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			calls = append(calls, ctx)
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/first")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/second")

	existing := client.NewMetadata(map[string][]string{
		"x-forwarded-for": {"10.0.0.1"},
	})
	ctx := client.NewContext(t.Context(), client.Info{Metadata: existing})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	assert.Len(t, calls, 2)
	for _, c := range calls {
		assert.Equal(t, []string{"10.0.0.1"}, client.FromContext(c).Metadata.Get("x-forwarded-for"))
	}
}

func TestLogsProcessor_MissingAttribute_SkipsSilently(t *testing.T) {
	var capturedCtx context.Context
	next := &mockLogsConsumer{
		consumeFunc: func(ctx context.Context, _ plog.Logs) error {
			capturedCtx = ctx
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty() // no attributes

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	clientInfo := client.FromContext(capturedCtx)
	assert.Nil(t, clientInfo.Metadata.Get("cwlogs.log_group"))
}

func TestLogsProcessor_ErrorsAreJoined(t *testing.T) {
	errFirst := errors.New("first failed")
	errSecond := errors.New("second failed")
	var callCount int
	next := &mockLogsConsumer{
		consumeFunc: func(_ context.Context, _ plog.Logs) error {
			callCount++
			if callCount == 1 {
				return errFirst
			}
			return errSecond
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/first")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/second")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.Error(t, err)
	assert.ErrorIs(t, err, errFirst)
	assert.ErrorIs(t, err, errSecond)
	assert.Equal(t, 2, callCount)
}

func TestLogsProcessor_PartialError(t *testing.T) {
	errFail := errors.New("downstream failed")
	var callCount int
	next := &mockLogsConsumer{
		consumeFunc: func(_ context.Context, _ plog.Logs) error {
			callCount++
			if callCount == 1 {
				return errFail
			}
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/first")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/second")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.ErrorIs(t, err, errFail)
	assert.Equal(t, 2, callCount, "should still call next for all groups")
}

func TestLogsProcessor_MultipleResourceLogs_SameMetadata(t *testing.T) {
	var callLogs []plog.Logs
	next := &mockLogsConsumer{
		consumeFunc: func(_ context.Context, ld plog.Logs) error {
			callLogs = append(callLogs, ld)
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	logs := plog.NewLogs()
	rl1 := logs.ResourceLogs().AppendEmpty()
	rl1.Resource().Attributes().PutStr("cwlogs.log_group", "/app")
	rl1.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("log1")
	rl2 := logs.ResourceLogs().AppendEmpty()
	rl2.Resource().Attributes().PutStr("cwlogs.log_group", "/app")
	rl2.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("log2")

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, logs)

	assert.NoError(t, err)
	assert.Len(t, callLogs, 1)
	// Should pass original data (both ResourceLogs) in a single call.
	assert.Equal(t, 2, callLogs[0].ResourceLogs().Len())
}

func TestLogsProcessor_EmptyInput(t *testing.T) {
	var called bool
	next := &mockLogsConsumer{
		consumeFunc: func(_ context.Context, ld plog.Logs) error {
			called = true
			assert.Equal(t, 0, ld.ResourceLogs().Len())
			return nil
		},
	}
	processor := newLogsProcessor(singleKeyCfg, next)

	ctx := client.NewContext(t.Context(), client.Info{})
	err := processor.ConsumeLogs(ctx, plog.NewLogs())

	assert.NoError(t, err)
	assert.True(t, called, "should still call next with empty payload")
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
