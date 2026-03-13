// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// OTel GenAI semantic conventions 1.39.0
// https://opentelemetry.io/docs/specs/semconv/gen-ai/
package common // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/genaiadapterconnector/adapters/common"

const (
	GenAIRequestModel             = "gen_ai.request.model"
	GenAIProviderName             = "gen_ai.provider.name"
	GenAIUsageInputTokens         = "gen_ai.usage.input_tokens"  //nolint:gosec // not credentials
	GenAIUsageOutputTokens        = "gen_ai.usage.output_tokens" //nolint:gosec // not credentials
	GenAISystemInstructions       = "gen_ai.system_instructions"
	GenAIToolDefinitions          = "gen_ai.tool.definitions"
	GenAIAgentName                = "gen_ai.agent.name"
	GenAIAgentID                  = "gen_ai.agent.id"
	GenAIAgentDescription         = "gen_ai.agent.description"
	GenAIConversationID           = "gen_ai.conversation.id"
	GenAIToolName                 = "gen_ai.tool.name"
	GenAIToolDescription          = "gen_ai.tool.description"
	GenAIToolCallArguments        = "gen_ai.tool.call.arguments"
	GenAIToolCallID               = "gen_ai.tool.call.id"
	GenAIToolCallResult           = "gen_ai.tool.call.result"
	GenAIResponseID               = "gen_ai.response.id"
	GenAIResponseModel            = "gen_ai.response.model"
	GenAIResponseFinishReason     = "gen_ai.response.finish_reasons"
	GenAIOperationName            = "gen_ai.operation.name"
	GenAIInputMessages            = "gen_ai.input.messages"
	GenAIOutputMessages           = "gen_ai.output.messages"
	GenAIRequestTemperature       = "gen_ai.request.temperature"
	GenAIRequestTopP              = "gen_ai.request.top_p"
	GenAIRequestMaxTokens         = "gen_ai.request.max_tokens" //nolint:gosec // not credentials
	GenAIRequestFreqPenalty       = "gen_ai.request.frequency_penalty"
	GenAIRequestPresPenalty       = "gen_ai.request.presence_penalty"
	GenAIRequestStopSeq           = "gen_ai.request.stop_sequences"
	GenAIUsageCacheReadTokens     = "gen_ai.usage.cache_read.input_tokens"     //nolint:gosec // not credentials
	GenAIUsageCacheCreationTokens = "gen_ai.usage.cache_creation.input_tokens" //nolint:gosec // not credentials
)

const (
	GenAIOpChat        = "chat"
	GenAIOpInvokeAgent = "invoke_agent"
	GenAIOpExecuteTool = "execute_tool"
	GenAIOpEmbeddings  = "embeddings"
)

const (
	GenAIProviderAnthropic   = "anthropic"
	GenAIProviderBedrock     = "aws.bedrock"
	GenAIProviderAzureInfer  = "azure.ai.inference"
	GenAIProviderAzureOpenAI = "azure.ai.openai"
	GenAIProviderCohere      = "cohere"
	GenAIProviderDeepSeek    = "deepseek"
	GenAIProviderGemini      = "gcp.gemini"
	GenAIProviderGCPGenAI    = "gcp.gen_ai"
	GenAIProviderVertexAI    = "gcp.vertex_ai"
	GenAIProviderGroq        = "groq"
	GenAIProviderWatsonX     = "ibm.watsonx.ai"
	GenAIProviderMistral     = "mistral_ai"
	GenAIProviderOpenAI      = "openai"
	GenAIProviderPerplexity  = "perplexity"
	GenAIProviderXAI         = "x_ai"
)
