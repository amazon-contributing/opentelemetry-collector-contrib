// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package adapters

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/genaiadapterconnector/adapters/common"
)

const otelSchemaBase = "https://opentelemetry.io/docs/specs/semconv/gen-ai"

func fetchOTelSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	return common.FetchJSONSchema(t, fmt.Sprintf("%s/%s.json", otelSchemaBase, name))
}

func validateOtelSchemaMessages(t *testing.T, messages []map[string]any, schemaName string) {
	t.Helper()
	common.ValidateArrayItems(t, messages, fetchOTelSchema(t, schemaName), schemaName)
}

func TestLangchain_SimpleChat(t *testing.T) {
	span := newSpan(langchainSimpleChatSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "anthropic.claude-3-haiku-20240307-v1:0", getAttr[string](attrs, common.GenAIRequestModel))
	assert.Equal(t, "chat", getAttr[string](attrs, common.GenAIOperationName))
	assert.Equal(t, common.GenAIProviderBedrock, getAttr[string](attrs, common.GenAIProviderName))
	assert.Equal(t, int64(12), getAttr[int64](attrs, common.GenAIUsageInputTokens))
	assert.Equal(t, int64(5), getAttr[int64](attrs, common.GenAIUsageOutputTokens))
	assert.True(t, hasAttr(attrs, common.GenAIInputMessages))
	assert.True(t, hasAttr(attrs, common.GenAIOutputMessages))

	var inputMsgs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs))
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")

	var outputMsgs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIOutputMessages)), &outputMsgs))
	validateOtelSchemaMessages(t, outputMsgs, "gen-ai-output-messages")

	assert.False(t, hasAttr(attrs, "openinference.span.kind"))
	assert.False(t, hasAttr(attrs, "llm.model_name"))
	assert.False(t, hasAttr(attrs, "llm.provider"))
	assert.False(t, hasAttr(attrs, "llm.invocation_parameters"))
	assert.False(t, hasAttr(attrs, "llm.token_count.prompt"))
	assert.False(t, hasAttr(attrs, "llm.input_messages.0.message.role"))
	assert.False(t, hasAttr(attrs, "output.value"))
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "input.mime_type"))
	assert.False(t, hasAttr(attrs, "output.mime_type"))
}

func TestLangchain_SystemMessage(t *testing.T) {
	span := newSpan(langchainSystemMessageSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 2)
	assert.Equal(t, "system", inputMsgs[0]["role"])
	assert.Equal(t, "You are a pirate.", getTextContent(t, inputMsgs[0]))
	assert.Equal(t, "user", inputMsgs[1]["role"])
	assert.Equal(t, "Say hello in one word", getTextContent(t, inputMsgs[1]))
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")
}

func TestLangchain_ToolCallOutput(t *testing.T) {
	span := newSpan(langchainToolCallOutputSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var outputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIOutputMessages)), &outputMsgs)
	require.NoError(t, err)
	require.Len(t, outputMsgs, 1)
	assert.Equal(t, "assistant", outputMsgs[0]["role"])

	toolCalls := getToolCallParts(t, outputMsgs[0])
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "get_weather", toolCalls[0]["name"])
	assert.Equal(t, "toolu_bdrk_01GdVCiXNUBY9jGNhN45bFrU", toolCalls[0]["id"])
	assert.True(t, hasAttr(attrs, common.GenAIToolDefinitions))
	validateOtelSchemaMessages(t, outputMsgs, "gen-ai-output-messages")
}

func TestLangchain_LLMWithToolResult(t *testing.T) {
	span := newSpan(langchainLLMWithToolResultSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 3)

	assert.Equal(t, "user", inputMsgs[0]["role"])
	assert.Equal(t, "assistant", inputMsgs[1]["role"])
	assert.Equal(t, "tool", inputMsgs[2]["role"])
	tcResp := getToolCallResponsePart(t, inputMsgs[2])
	assert.Equal(t, "72F and sunny in Seattle", tcResp["response"])
	assert.Equal(t, "toolu_bdrk_01GdVCiXNUBY9jGNhN45bFrU", tcResp["id"])

	toolCalls := getToolCallParts(t, inputMsgs[1])
	require.Len(t, toolCalls, 1)
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")
}

func TestLangchain_ToolExecution(t *testing.T) {
	span := newSpan(langchainToolSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "execute_tool", getAttr[string](attrs, common.GenAIOperationName))
	assert.Equal(t, "get_weather", getAttr[string](attrs, common.GenAIToolName))
	assert.Equal(t, "Get the weather for a city.", getAttr[string](attrs, common.GenAIToolDescription))
	assert.Equal(t, "{'city': 'Seattle'}", getAttr[string](attrs, common.GenAIToolCallArguments))

	assert.False(t, hasAttr(attrs, "tool.name"))
	assert.False(t, hasAttr(attrs, "tool.description"))
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "output.value"))
}

func TestLangchain_AgentSpan(t *testing.T) {
	span := newSpan(langchainAgentSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "invoke_agent", getAttr[string](attrs, common.GenAIOperationName))
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "output.value"))
}

func TestLangchain_ChainOutputWithMetadata(t *testing.T) {
	span := newSpan(langchainChainOutputWithMetadataSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, int64(403), getAttr[int64](attrs, common.GenAIUsageInputTokens))
	assert.Equal(t, int64(15), getAttr[int64](attrs, common.GenAIUsageOutputTokens))
	assert.Equal(t, "anthropic.claude-3-haiku-20240307-v1:0", getAttr[string](attrs, common.GenAIRequestModel))
	assert.Equal(t, "end_turn", getAttr[string](attrs, common.GenAIResponseFinishReason))

	var outputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIOutputMessages)), &outputMsgs)
	require.NoError(t, err)
	require.Len(t, outputMsgs, 1)
	assert.Equal(t, "assistant", outputMsgs[0]["role"])
	assert.Equal(t, "The weather in Seattle is 72F and sunny.", getTextContent(t, outputMsgs[0]))
	validateOtelSchemaMessages(t, outputMsgs, "gen-ai-output-messages")
}

func TestLangchain_ChainOutputWithToolCall(t *testing.T) {
	span := newSpan(langchainChainOutputWithToolCallSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "tool_use", getAttr[string](attrs, common.GenAIResponseFinishReason))

	var outputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIOutputMessages)), &outputMsgs)
	require.NoError(t, err)
	require.Len(t, outputMsgs, 1)

	toolCalls := getToolCallParts(t, outputMsgs[0])
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "get_weather", toolCalls[0]["name"])
	validateOtelSchemaMessages(t, outputMsgs, "gen-ai-output-messages")
}

func TestLangchain_ChainToolsNode(t *testing.T) {
	span := newSpan(langchainChainToolsNodeSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "get_clinic_hours", getAttr[string](attrs, common.GenAIToolName))
	assert.Equal(t, "toolu_bdrk_01McWSdPrfhjsHiutBbjmgDK", getAttr[string](attrs, common.GenAIToolCallID))
	assert.Equal(t, "Monday-Friday: 8AM-6PM, Saturday: 9AM-4PM, Sunday: Closed.", getAttr[string](attrs, common.GenAIToolCallResult))
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "output.value"))
}

func TestLangchain_ChainOutputArrayFormat(t *testing.T) {
	span := newSpan(langchainChainOutputArrayFormatSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 2)
	assert.Equal(t, "user", inputMsgs[0]["role"])
	assert.Equal(t, "assistant", inputMsgs[1]["role"])
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")
}

func TestLangchain_PromptSpan(t *testing.T) {
	span := newSpan(langchainPromptSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.False(t, hasAttr(attrs, common.GenAIOperationName))
	assert.False(t, hasAttr(attrs, "openinference.span.kind"))
	assert.True(t, hasAttr(attrs, "llm.prompt_template.template"))
	assert.True(t, hasAttr(attrs, "llm.prompt_template.variables"))
}

func TestLangchain_Chain(t *testing.T) {
	span := newSpan(langchainChainSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "input.mime_type"))
	assert.False(t, hasAttr(attrs, "output.mime_type"))
}

func TestBedrock_ConverseLLM(t *testing.T) {
	span := newSpan(bedrockConverseLLMSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "chat", getAttr[string](attrs, common.GenAIOperationName))
	assert.Equal(t, "anthropic.claude-3-haiku-20240307-v1:0", getAttr[string](attrs, common.GenAIRequestModel))
	assert.Equal(t, int64(12), getAttr[int64](attrs, common.GenAIUsageInputTokens))
	assert.Equal(t, int64(5), getAttr[int64](attrs, common.GenAIUsageOutputTokens))
	assert.Equal(t, 100.0, getAttr[float64](attrs, common.GenAIRequestMaxTokens))
	assert.Equal(t, 0.7, getAttr[float64](attrs, common.GenAIRequestTemperature))

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 1)
	assert.Equal(t, "user", inputMsgs[0]["role"])
	assert.Equal(t, "Say hello in one word", getTextContent(t, inputMsgs[0]))
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")

	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "output.value"))
}

func TestBedrock_ConverseWithSystem(t *testing.T) {
	span := newSpan(bedrockConverseWithSystemSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 2)
	assert.Equal(t, "system", inputMsgs[0]["role"])
	assert.Equal(t, "You are a pirate.", getTextContent(t, inputMsgs[0]))
	assert.Equal(t, "user", inputMsgs[1]["role"])
	assert.Equal(t, "Say hello in one word", getTextContent(t, inputMsgs[1]))
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")
}

func TestLlamaindex_LLM(t *testing.T) {
	span := newSpan(llamaindexLLMSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "chat", getAttr[string](attrs, common.GenAIOperationName))
	assert.Equal(t, "anthropic.claude-3-haiku-20240307-v1:0", getAttr[string](attrs, common.GenAIRequestModel))
	assert.Equal(t, int64(12), getAttr[int64](attrs, common.GenAIUsageInputTokens))
	assert.Equal(t, int64(5), getAttr[int64](attrs, common.GenAIUsageOutputTokens))

	assert.Equal(t, int64(0), getAttr[int64](attrs, common.GenAIUsageCacheReadTokens))
	assert.Equal(t, int64(0), getAttr[int64](attrs, common.GenAIUsageCacheCreationTokens))
	assert.False(t, hasAttr(attrs, "llm.token_count.prompt_details.cache_read"))
	assert.False(t, hasAttr(attrs, "llm.token_count.prompt_details.cache_write"))
	assert.False(t, hasAttr(attrs, "llm.token_count.total"))

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 1)
	assert.Equal(t, "user", inputMsgs[0]["role"])
}

func TestLlamaindex_SystemMessage(t *testing.T) {
	span := newSpan(llamaindexSystemMessageSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 2)
	assert.Equal(t, "system", inputMsgs[0]["role"])
	assert.Equal(t, "user", inputMsgs[1]["role"])
}

func TestLangchain_ShouldContinue(t *testing.T) {
	span := newSpan(langchainShouldContinueSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.False(t, hasAttr(attrs, "output.value"))
	assert.False(t, hasAttr(attrs, "metadata"))
}

func TestLangchain_LangGraphTopLevel(t *testing.T) {
	span := newSpan(langchainLangGraphTopLevelSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.True(t, hasAttr(attrs, common.GenAIInputMessages))
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "input.mime_type"))
}

func TestBedrock_ConverseToolUse(t *testing.T) {
	span := newSpan(bedrockConverseToolUseSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "chat", getAttr[string](attrs, common.GenAIOperationName))
	assert.Equal(t, int64(331), getAttr[int64](attrs, common.GenAIUsageInputTokens))
	assert.Equal(t, int64(53), getAttr[int64](attrs, common.GenAIUsageOutputTokens))
	assert.Equal(t, 200.0, getAttr[float64](attrs, common.GenAIRequestMaxTokens))

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 1)
	assert.Equal(t, "user", inputMsgs[0]["role"])
	assert.Equal(t, "What's the weather in Seattle?", getTextContent(t, inputMsgs[0]))
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")
}

func TestCrewai_CrewKickoff(t *testing.T) {
	span := newSpan(crewaiCrewKickoffSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.True(t, hasAttr(attrs, "crew_agents"))
	assert.True(t, hasAttr(attrs, "crew_id"))
	assert.True(t, hasAttr(attrs, "crew_key"))
	assert.True(t, hasAttr(attrs, "crew_tasks"))
}

func TestCrewai_CrewCreated(t *testing.T) {
	span := newSpan(crewaiCrewCreatedSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.True(t, hasAttr(attrs, "crew_fingerprint"))
	assert.True(t, hasAttr(attrs, "crew_memory"))
	assert.True(t, hasAttr(attrs, "crewai_version"))
	assert.True(t, hasAttr(attrs, "python_version"))
}

func TestCrewai_TaskCreated(t *testing.T) {
	span := newSpan(crewaiTaskCreatedSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.True(t, hasAttr(attrs, "agent_role"))
	assert.True(t, hasAttr(attrs, "task_id"))
	assert.True(t, hasAttr(attrs, "task_key"))
}

func TestTransform_ToolWithParameters(t *testing.T) {
	span := newSpan(toolWithParametersSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "get_weather", getAttr[string](attrs, common.GenAIToolName))
	assert.Equal(t, "Get weather for a city", getAttr[string](attrs, common.GenAIToolDescription))
	assert.Equal(t, `{"city": "Seattle"}`, getAttr[string](attrs, common.GenAIToolCallArguments))
	assert.Equal(t, "72F and sunny in Seattle", getAttr[string](attrs, common.GenAIToolCallResult))
	assert.False(t, hasAttr(attrs, "tool.parameters"))
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "output.value"))
}

func TestLangchain_ToolSpan(t *testing.T) {
	span := newSpan(langchainToolSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "execute_tool", getAttr[string](attrs, common.GenAIOperationName))
	assert.Equal(t, "get_weather", getAttr[string](attrs, common.GenAIToolName))
	assert.Equal(t, "Get the weather for a city.", getAttr[string](attrs, common.GenAIToolDescription))
	assert.Equal(t, "{'city': 'Seattle'}", getAttr[string](attrs, common.GenAIToolCallArguments))
	assert.Contains(t, getAttr[string](attrs, common.GenAIToolCallResult), "72F and sunny in Seattle")
	assert.False(t, hasAttr(attrs, "input.value"))
	assert.False(t, hasAttr(attrs, "output.value"))
	assert.False(t, hasAttr(attrs, "input.mime_type"))
	assert.False(t, hasAttr(attrs, "output.mime_type"))
	assert.False(t, hasAttr(attrs, "metadata"))
}

func TestTransform_AttributesRemoved(t *testing.T) {
	span := newSpan(attributesToRemoveSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, "gpt-4", getAttr[string](attrs, common.GenAIRequestModel))

	removedKeys := []string{
		"input.mime_type", "output.mime_type",
		"metadata", "tool.parameters", "llm.system", "llm.token_count.total",
		"llm.token_count.prompt_details.cache_read",
		"llm.token_count.prompt_details.cache_write",
	}
	for _, key := range removedKeys {
		assert.False(t, hasAttr(attrs, key), "expected %q to be removed", key)
	}

	keptKeys := []string{
		"llm.prompt_template.template", "llm.prompt_template.variables",
		"llm.cost.total", "llm.cost.prompt", "llm.cost.completion",
		"llm.function_call", "llm.prompts",
		"llm.token_count.prompt_details.audio",
		"llm.token_count.completion_details.reasoning",
		"llm.token_count.completion_details.audio",
	}
	for _, key := range keptKeys {
		assert.True(t, hasAttr(attrs, key), "expected %q to be kept", key)
	}
}

func TestTransform_MultimodalContent(t *testing.T) {
	span := newSpan(multimodalContentSpan)
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	var inputMsgs []map[string]any
	err := json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIInputMessages)), &inputMsgs)
	require.NoError(t, err)
	require.Len(t, inputMsgs, 1)
	assert.Equal(t, "user", inputMsgs[0]["role"])
	assert.Equal(t, "What's in this image?", getTextContent(t, inputMsgs[0]))

	var outputMsgs []map[string]any
	err = json.Unmarshal([]byte(getAttr[string](attrs, common.GenAIOutputMessages)), &outputMsgs)
	require.NoError(t, err)
	require.Len(t, outputMsgs, 1)
	assert.Equal(t, "I see a cat.", getTextContent(t, outputMsgs[0]))
	validateOtelSchemaMessages(t, inputMsgs, "gen-ai-input-messages")
	validateOtelSchemaMessages(t, outputMsgs, "gen-ai-output-messages")
}

func TestTransform_ProviderMapping(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		expected string
	}{
		{"amazon_bedrock", "amazon_bedrock", common.GenAIProviderBedrock},
		{"bedrock_converse", "bedrock_converse", common.GenAIProviderBedrock},
		{"aws", "aws", common.GenAIProviderBedrock},
		{"bedrock", "bedrock", common.GenAIProviderBedrock},
		{"openai", "openai", common.GenAIProviderOpenAI},
		{"anthropic", "anthropic", common.GenAIProviderAnthropic},
		{"azure_openai", "azure_openai", common.GenAIProviderAzureOpenAI},
		{"azure", "azure", common.GenAIProviderAzureOpenAI},
		{"azure_ai", "azure_ai", common.GenAIProviderAzureOpenAI},
		{"mistral", "mistral", common.GenAIProviderMistral},
		{"mistralai", "mistralai", common.GenAIProviderMistral},
		{"google", "google", common.GenAIProviderGCPGenAI},
		{"google_genai", "google_genai", common.GenAIProviderGCPGenAI},
		{"google_vertexai", "google_vertexai", common.GenAIProviderVertexAI},
		{"vertex", "vertex", common.GenAIProviderVertexAI},
		{"vertexai", "vertexai", common.GenAIProviderVertexAI},
		{"cohere", "cohere", common.GenAIProviderCohere},
		{"deepseek", "deepseek", common.GenAIProviderDeepSeek},
		{"gemini", "gemini", common.GenAIProviderGemini},
		{"groq", "groq", common.GenAIProviderGroq},
		{"perplexity", "perplexity", common.GenAIProviderPerplexity},
		{"xai", "xai", common.GenAIProviderXAI},
		{"x_ai", "x_ai", common.GenAIProviderXAI},
		{"unknown_provider", "some_unknown", "some_unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := newSpan(map[string]any{
				"openinference.span.kind": "LLM",
				"llm.provider":            tt.provider,
			})
			TransformOpenInferenceSpan(span)
			assert.Equal(t, tt.expected, getAttr[string](span.Attributes(), common.GenAIProviderName))
		})
	}
}

func TestTransform_LLMSystemFallback(t *testing.T) {
	span := newSpan(map[string]any{
		"openinference.span.kind": "LLM",
		"llm.system":              "openai",
	})
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, common.GenAIProviderOpenAI, getAttr[string](attrs, common.GenAIProviderName))
	assert.False(t, hasAttr(attrs, "llm.system"))
}

func TestTransform_LLMSystemDoesNotOverrideProvider(t *testing.T) {
	span := newSpan(map[string]any{
		"openinference.span.kind": "LLM",
		"llm.provider":            "amazon_bedrock",
		"llm.system":              "openai",
	})
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, common.GenAIProviderBedrock, getAttr[string](attrs, common.GenAIProviderName))
	assert.False(t, hasAttr(attrs, "llm.system"))
}

func TestTransform_OperationMapping(t *testing.T) {
	tests := []struct {
		oiKind   string
		expected string
	}{
		{"LLM", "chat"},
		{"AGENT", "invoke_agent"},
		{"TOOL", "execute_tool"},
		{"EMBEDDING", "embeddings"},
	}
	for _, tt := range tests {
		t.Run(tt.oiKind, func(t *testing.T) {
			span := newSpan(map[string]any{"openinference.span.kind": tt.oiKind})
			TransformOpenInferenceSpan(span)
			assert.Equal(t, tt.expected, getAttr[string](span.Attributes(), common.GenAIOperationName))
		})
	}
}

func TestTransform_EmptySpan(t *testing.T) {
	span := newSpan(map[string]any{})
	TransformOpenInferenceSpan(span)
	assert.Equal(t, 0, span.Attributes().Len())
}

func TestTransform_SetIfAbsentPreventsOverwrite(t *testing.T) {
	span := newSpan(map[string]any{
		"openinference.span.kind":    "CHAIN",
		"llm.token_count.prompt":     int64(100),
		"llm.token_count.completion": int64(50),
		"output.value":               `{"messages": [{"type": "ai", "content": "test", "additional_kwargs": {"usage": {"prompt_tokens": 999, "completion_tokens": 888}}}]}`,
	})
	TransformOpenInferenceSpan(span)
	attrs := span.Attributes()

	assert.Equal(t, int64(100), getAttr[int64](attrs, common.GenAIUsageInputTokens))
	assert.Equal(t, int64(50), getAttr[int64](attrs, common.GenAIUsageOutputTokens))
}

// getTextContent extracts the text content from a semconv message's parts array.
func getTextContent(t *testing.T, msg map[string]any) string {
	t.Helper()
	parts, ok := msg["parts"].([]any)
	require.True(t, ok, "message missing parts array")
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if part["type"] == "text" {
			content, _ := part["content"].(string)
			return content
		}
	}
	t.Fatal("no text part found in message")
	return ""
}

// getToolCallParts extracts tool_call parts from a semconv message's parts array.
func getToolCallParts(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	parts, ok := msg["parts"].([]any)
	require.True(t, ok, "message missing parts array")
	var toolCalls []map[string]any
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if part["type"] == "tool_call" {
			toolCalls = append(toolCalls, part)
		}
	}
	return toolCalls
}

// getToolCallResponsePart extracts the first tool_call_response part from a message.
func getToolCallResponsePart(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	parts, ok := msg["parts"].([]any)
	require.True(t, ok, "message missing parts array")
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if part["type"] == "tool_call_response" {
			return part
		}
	}
	t.Fatal("no tool_call_response part found in message")
	return nil
}

func newSpan(attrs map[string]any) ptrace.Span {
	td := ptrace.NewTraces()
	span := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			span.Attributes().PutStr(k, val)
		case int64:
			span.Attributes().PutInt(k, val)
		case float64:
			span.Attributes().PutDouble(k, val)
		case int:
			span.Attributes().PutInt(k, int64(val))
		}
	}
	return span
}

func getAttr[T string | int64 | float64](attrs pcommon.Map, key string) T {
	v, ok := attrs.Get(key)
	if !ok {
		var zero T
		return zero
	}
	var zero T
	switch any(zero).(type) {
	case string:
		return any(v.AsString()).(T)
	case int64:
		return any(v.Int()).(T)
	case float64:
		return any(v.Double()).(T)
	}
	return zero
}

func hasAttr(attrs pcommon.Map, key string) bool {
	_, ok := attrs.Get(key)
	return ok
}
