// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Transforms OpenInference spans to OTel GenAI semantic conventions for CloudWatch.
//
// OpenInference spec: https://arize-ai.github.io/openinference/spec/semantic_conventions.html
// OTel GenAI semconv:  https://opentelemetry.io/docs/specs/semconv/gen-ai/
package adapters // import "github.com/open-telemetry/opentelemetry-collector-contrib/connector/genaiadapterconnector/adapters"

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/open-telemetry/opentelemetry-collector-contrib/connector/genaiadapterconnector/adapters/common"
)

var (

	// https://arize-ai.github.io/openinference/spec/semantic_conventions.html#reserved-attributes
	// ordered by priority: when two source keys map to the same target, the first one wins.
	attributeMap = []struct{ from, to string }{
		{"llm.provider", common.GenAIProviderName},
		{"llm.system", common.GenAIProviderName},
		{"llm.model_name", common.GenAIRequestModel},
		{"embedding.model_name", common.GenAIRequestModel},
		{"llm.token_count.prompt", common.GenAIUsageInputTokens},
		{"llm.token_count.completion", common.GenAIUsageOutputTokens},
		{"llm.tools", common.GenAIToolDefinitions},
		{"agent.name", common.GenAIAgentName},
		{"graph.node.id", common.GenAIAgentName},
		{"agent.id", common.GenAIAgentID},
		{"agent.description", common.GenAIAgentDescription},
		{"session.id", common.GenAIConversationID},
		{"tool.name", common.GenAIToolName},
		{"tool.description", common.GenAIToolDescription},
		{"tool.id", common.GenAIToolCallID},
		{"llm.token_count.prompt_details.cache_read", common.GenAIUsageCacheReadTokens},
		{"llm.token_count.prompt_details.cache_write", common.GenAIUsageCacheCreationTokens},
	}

	// https://arize-ai.github.io/openinference/spec/semantic_conventions.html#reserved-attributes
	// under llm.invocations
	invocationParamMap = map[string]string{
		"temperature":       common.GenAIRequestTemperature,
		"top_p":             common.GenAIRequestTopP,
		"topP":              common.GenAIRequestTopP,
		"max_tokens":        common.GenAIRequestMaxTokens,
		"maxTokens":         common.GenAIRequestMaxTokens,
		"frequency_penalty": common.GenAIRequestFreqPenalty,
		"presence_penalty":  common.GenAIRequestPresPenalty,
		"stop":              common.GenAIRequestStopSeq,
		"stop_sequences":    common.GenAIRequestStopSeq,
		"stopSequences":     common.GenAIRequestStopSeq,
	}

	// https://arize-ai.github.io/openinference/spec/semantic_conventions.html#span-kinds
	operationMap = map[string]string{
		"LLM":       common.GenAIOpChat,
		"AGENT":     common.GenAIOpInvokeAgent,
		"TOOL":      common.GenAIOpExecuteTool,
		"EMBEDDING": common.GenAIOpEmbeddings,
	}

	// mapping for llm.system and llm.provider
	providerMap = map[string]string{
		"amazon_bedrock":   common.GenAIProviderBedrock,
		"aws":              common.GenAIProviderBedrock,
		"bedrock":          common.GenAIProviderBedrock,
		"bedrock_converse": common.GenAIProviderBedrock,
		"azure":            common.GenAIProviderAzureOpenAI,
		"azure_ai":         common.GenAIProviderAzureOpenAI,
		"azure_openai":     common.GenAIProviderAzureOpenAI,
		"google":           common.GenAIProviderGCPGenAI,
		"google_genai":     common.GenAIProviderGCPGenAI,
		"google_vertexai":  common.GenAIProviderVertexAI,
		"vertex":           common.GenAIProviderVertexAI,
		"vertexai":         common.GenAIProviderVertexAI,
		"mistral":          common.GenAIProviderMistral,
		"mistralai":        common.GenAIProviderMistral,
		"openai":           common.GenAIProviderOpenAI,
		"anthropic":        common.GenAIProviderAnthropic,
		"cohere":           common.GenAIProviderCohere,
		"deepseek":         common.GenAIProviderDeepSeek,
		"gemini":           common.GenAIProviderGemini,
		"groq":             common.GenAIProviderGroq,
		"perplexity":       common.GenAIProviderPerplexity,
		"xai":              common.GenAIProviderXAI,
		"x_ai":             common.GenAIProviderXAI,
	}

	// attributes that are essentially just massive JSON dumps
	attributesToRemove = map[string]bool{
		"input.value":           true,
		"output.value":          true,
		"input.mime_type":       true,
		"output.mime_type":      true,
		"metadata":              true,
		"tool.parameters":       true,
		"llm.token_count.total": true, // this can be calculated from input + output token count
	}

	// attributes with no OTel semantic convention (1.39) equivalent.
	// kept on the span to avoid dropping customer data.
	// TODO: remove once we have a direct OTel translation.
	// attributesNoOTelEquivalent = map[string]bool{
	// 	"llm.function_call":             true,
	// 	"llm.prompts":                   true,
	// 	"llm.prompt_template.template":  true,
	// 	"llm.prompt_template.variables": true,
	// 	"llm.token_count.prompt_details.audio":         true,
	// 	"llm.token_count.completion_details.reasoning": true,
	// 	"llm.token_count.completion_details.audio":     true,
	// 	"llm.cost.prompt":     true,
	// 	"llm.cost.completion": true,
	// 	"llm.cost.total":      true,
	// }

	// not a part of the OpenInference spec but comes from
	// instrumentation library's metadata which may contain information about
	// the role of the message input/output
	roleMap = map[string]string{
		"human":  "user",
		"ai":     "assistant",
		"system": "system",
		"tool":   "tool",
	}
	inputMsgPattern     = regexp.MustCompile(`^llm\.input_messages\.(\d+)\.message\.(.+)$`)
	outputMsgPattern    = regexp.MustCompile(`^llm\.output_messages\.(\d+)\.message\.(.+)$`)
	toolsPattern        = regexp.MustCompile(`^llm\.tools\.(\d+)\.tool\.json_schema$`)
	toolCallKeyPattern  = regexp.MustCompile(`^tool_calls\.(\d+)\.tool_call\.(.+)$`)
	contentBlockPattern = regexp.MustCompile(`^contents\.(\d+)\.message_content\.(.+)$`)
)

func TransformOpenInferenceSpan(span ptrace.Span) {
	attrs := span.Attributes()
	// maps the llm input message index to raw OpenInference attributes
	// example: 0: {"role": "system", "content": "You are a helpful assistant"},
	//          1: {"role": "user", "content": "What's the weather?"},
	//          2: {"role": "tool", "content": "72°F and sunny", "tool_call_id": "call_abc123"},
	inputMessages := make(map[int]map[string]any)
	outputMessages := make(map[int]map[string]any)

	// maps the tool index to its JSON schema string
	// example: 0: '{"type":"function","function":{"name":"get_weather","parameters":{...}}}',
	//          1: '{"type":"function","function":{"name":"search","parameters":{...}}}',
	tools := make(map[int]string)

	toRemove := []string{}

	spanKind, _ := attrs.Get("openinference.span.kind")
	spanKindStr := spanKind.Str()

	attrs.Range(func(key string, value pcommon.Value) bool {
		switch {
		case key == "openinference.span.kind":
			if op, ok := operationMap[value.Str()]; ok {
				attrs.PutStr(common.GenAIOperationName, op)
			}
			toRemove = append(toRemove, key)
		case inputMsgPattern.MatchString(key):
			// see: https://arize-ai.github.io/openinference/spec/semantic_conventions.html#llm-inputoutput-messages
			// e.g. llm.input_messages.2.message.role → match[1]="2", match[2]="role"
			match := inputMsgPattern.FindStringSubmatch(key)
			idx, _ := strconv.Atoi(match[1])
			if inputMessages[idx] == nil {
				inputMessages[idx] = make(map[string]any)
			}
			inputMessages[idx][match[2]] = value.AsRaw()
			toRemove = append(toRemove, key)
		case outputMsgPattern.MatchString(key):
			// see: https://arize-ai.github.io/openinference/spec/semantic_conventions.html#llm-inputoutput-messages
			match := outputMsgPattern.FindStringSubmatch(key)
			idx, _ := strconv.Atoi(match[1])
			if outputMessages[idx] == nil {
				outputMessages[idx] = make(map[string]any)
			}
			outputMessages[idx][match[2]] = value.AsRaw()
			toRemove = append(toRemove, key)
		case toolsPattern.MatchString(key):
			// see: https://arize-ai.github.io/openinference/spec/semantic_conventions.html#available-tools
			match := toolsPattern.FindStringSubmatch(key)
			idx, _ := strconv.Atoi(match[1])
			tools[idx] = value.Str()
			toRemove = append(toRemove, key)
		case key == "llm.invocation_parameters":
			parseInvocationParams(value.Str(), attrs)
			toRemove = append(toRemove, key)
		case key == "output.value":
			parseOutputValue(value.Str(), spanKindStr, attrs)
			toRemove = append(toRemove, key)
		case key == "input.value":
			parseInputValue(value.Str(), spanKindStr, attrs)
			toRemove = append(toRemove, key)
		case attributesToRemove[key]:
			toRemove = append(toRemove, key)
		}
		return true
	})

	// iterate backwards so top-down order is respected
	for i := len(attributeMap) - 1; i >= 0; i-- {
		m := attributeMap[i]
		if val, ok := attrs.Get(m.from); ok {
			mapAttribute(m.to, val, attrs)
			toRemove = append(toRemove, m.from)
		}
	}

	for _, key := range toRemove {
		attrs.Remove(key)
	}

	if len(inputMessages) > 0 {
		if data, err := json.Marshal(common.ParseJSON(convertMessages(inputMessages, false, ""), common.MaxJSONDepth)); err == nil {
			attrs.PutStr(common.GenAIInputMessages, string(data))
		}
	}
	if len(outputMessages) > 0 {
		var finishReason string
		if v, ok := attrs.Get(common.GenAIResponseFinishReason); ok {
			finishReason = v.AsString()
		}
		if data, err := json.Marshal(common.ParseJSON(convertMessages(outputMessages, true, finishReason), common.MaxJSONDepth)); err == nil {
			attrs.PutStr(common.GenAIOutputMessages, string(data))
		}
	}
	if len(tools) > 0 {
		keys := make([]int, 0, len(tools))
		for k := range tools {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		toolList := make([]string, len(keys))
		for i, k := range keys {
			toolList[i] = tools[k]
		}
		attrs.PutStr(common.GenAIToolDefinitions, "["+strings.Join(toolList, ",")+"]")
	}
}

func mapAttribute(newKey string, value pcommon.Value, attrs pcommon.Map) {
	switch value.Type() {
	case pcommon.ValueTypeInt:
		attrs.PutInt(newKey, value.Int())
	case pcommon.ValueTypeDouble:
		attrs.PutDouble(newKey, value.Double())
	case pcommon.ValueTypeBool:
		attrs.PutBool(newKey, value.Bool())
	case pcommon.ValueTypeStr:
		s := value.Str()
		if s == "" {
			return
		}
		if newKey == common.GenAIProviderName {
			if mapped, ok := providerMap[s]; ok {
				s = mapped
			}
		}
		attrs.PutStr(newKey, s)
	default:
		value.CopyTo(attrs.PutEmpty(newKey))
	}
}

func parseInvocationParams(value string, attrs pcommon.Map) {
	// maps and extracts individual OTel GenAI model request attributes from llm.invocation_parameters.
	// example: '{"model_name": "gpt-3", "temperature": 0.7, "max_tokens": 1000}'
	var params map[string]any
	if err := json.Unmarshal([]byte(value), &params); err != nil {
		return
	}
	for param, otelKey := range invocationParamMap {
		if v, ok := params[param]; ok {
			if f, ok := common.ParseFloat(v); ok {
				attrs.PutDouble(otelKey, f)
			} else if s, ok := common.ParseStr(v); ok {
				attrs.PutStr(otelKey, s)
			}
		}
	}
}

func parseInputValue(value, spanKind string, attrs pcommon.Map) {
	switch spanKind {
	case "TOOL":
		setIfAbsent(attrs, common.GenAIToolCallArguments, pcommon.NewValueStr(value))
	case "CHAIN":
		// no deep parsing needed here unlike output.value chain input.value never
		// contains unique metadata that's not already captured. if the input has
		// messages, they're already captured by llm.input_messages.* on this span.
		// only extract tool_call routing info from LangChain's tools node.
		var input map[string]any
		if err := json.Unmarshal([]byte(value), &input); err != nil {
			return
		}
		if toolCall, ok := input["tool_call"].(map[string]any); ok {
			if name, ok := common.ParseStr(toolCall["name"]); ok {
				setIfAbsent(attrs, common.GenAIToolName, pcommon.NewValueStr(name))
			}
			if id, ok := common.ParseStr(toolCall["id"]); ok {
				setIfAbsent(attrs, common.GenAIToolCallID, pcommon.NewValueStr(id))
			}
			if args, ok := toolCall["args"]; ok {
				if s, ok := common.ParseStr(args); ok {
					setIfAbsent(attrs, common.GenAIToolCallArguments, pcommon.NewValueStr(s))
				}
			}
		}
	}
}

func parseOutputValue(value, spanKind string, attrs pcommon.Map) {
	switch spanKind {
	case "TOOL":
		setIfAbsent(attrs, common.GenAIToolCallResult, pcommon.NewValueStr(value))
	case "CHAIN":
		parseChainOutput(value, attrs)
	}
}

func parseChainOutput(value string, attrs pcommon.Map) {
	// LangChain chain output.value embeds metadata not available elsewhere on the span
	// inside additional_kwargs, we deep-parse it here to extract and surface that data as OTel attributes.
	var output map[string]any
	// {"messages": [{
	//   "type": "ai",
	//   "content": "The weather in Seattle is 72°F and sunny.",
	//   "additional_kwargs": {
	//     "usage": {"prompt_tokens": 332, "completion_tokens": 53, "total_tokens": 385},
	//     "stop_reason": "tool_use",
	//     "model_id": "anthropic.claude-3-haiku-20240307-v1:0"
	//   },
	//   "tool_calls": [{"name": "get_weather", "args": {"city": "Seattle"}, "id": "toolu_bdrk_01TEmaN8xJN9snzDATRJvcFm"}]
	// }]}
	if err := json.Unmarshal([]byte(value), &output); err == nil {
		if messages, ok := output["messages"].([]any); ok && len(messages) > 0 {
			// always exactly a list of one, see:
			// https://github.com/langchain-ai/langgraph/blob/8fbdb144876ec9ca75943c7addb452a2bb634304/libs/prebuilt/langgraph/prebuilt/chat_agent_executor.py#L661-L662
			if msg, ok := messages[0].(map[string]any); ok {
				if addlKwargs, ok := msg["additional_kwargs"].(map[string]any); ok {
					if usage, ok := addlKwargs["usage"].(map[string]any); ok {
						if v, ok := common.ParseInt(usage["prompt_tokens"]); ok {
							setIfAbsent(attrs, common.GenAIUsageInputTokens, pcommon.NewValueInt(v))
						}
						if v, ok := common.ParseInt(usage["completion_tokens"]); ok {
							setIfAbsent(attrs, common.GenAIUsageOutputTokens, pcommon.NewValueInt(v))
						}
					}
					if v, ok := common.ParseStr(addlKwargs["model_id"]); ok {
						setIfAbsent(attrs, common.GenAIRequestModel, pcommon.NewValueStr(v))
					}
					if v, ok := common.ParseStr(addlKwargs["model_name"]); ok {
						setIfAbsent(attrs, common.GenAIResponseModel, pcommon.NewValueStr(v))
					}
					if v, ok := common.ParseStr(addlKwargs["stop_reason"]); ok {
						setIfAbsent(attrs, common.GenAIResponseFinishReason, pcommon.NewValueStr(v))
					}
				}

				if msgType, ok := msg["type"].(string); ok && msgType == "ai" {
					otelMsg := map[string]any{"role": "assistant"}
					var parts []map[string]any
					if content, ok := msg["content"].(string); ok && content != "" {
						parts = append(parts, map[string]any{"type": "text", "content": content})
					}
					if toolCalls, ok := msg["tool_calls"].([]any); ok {
						for _, tc := range toolCalls {
							if tcMap, ok := tc.(map[string]any); ok {
								part := map[string]any{"type": "tool_call"}
								if name, ok := common.ParseStr(tcMap["name"]); ok {
									part["name"] = name
								}
								if id, ok := common.ParseStr(tcMap["id"]); ok {
									part["id"] = id
								}
								if args, ok := tcMap["args"]; ok {
									part["arguments"] = args
								}
								parts = append(parts, part)
							}
						}
					}
					if len(parts) == 0 {
						parts = []map[string]any{}
					}
					otelMsg["parts"] = parts
					otelMsg["finish_reason"] = "stop"
					for _, p := range parts {
						if p["type"] == "tool_call" {
							otelMsg["finish_reason"] = "tool_call"
							break
						}
					}
					if data, err := json.Marshal(common.ParseJSON([]map[string]any{otelMsg}, common.MaxJSONDepth)); err == nil {
						setIfAbsent(attrs, common.GenAIOutputMessages, pcommon.NewValueStr(string(data)))
					}
				} else if msgType == "tool" {
					if content, ok := msg["content"].(string); ok {
						setIfAbsent(attrs, common.GenAIToolCallResult, pcommon.NewValueStr(content))
					}
				}
			}
		}
		return
	}

	// [
	//   {"type": "human", "content": "What's the weather in Seattle?", "additional_kwargs": {}, "name": null, "id": "f6dfa086-..."},
	//   {"type": "ai", "content": "The weather is 72°F.", "additional_kwargs": {"usage": {...}}, "tool_calls": []}
	// ]
	var messages []map[string]any
	if err := json.Unmarshal([]byte(value), &messages); err == nil && len(messages) > 0 {
		var converted []map[string]any
		for _, msg := range messages {
			otelMsg := make(map[string]any)
			if msgType, ok := msg["type"].(string); ok {
				if role, ok := roleMap[msgType]; ok {
					otelMsg["role"] = role
				} else {
					otelMsg["role"] = msgType
				}
			}
			var parts []map[string]any
			if content, ok := msg["content"].(string); ok && content != "" {
				parts = append(parts, map[string]any{"type": "text", "content": content})
			}
			if toolCalls, ok := msg["tool_calls"].([]any); ok {
				for _, tc := range toolCalls {
					if tcMap, ok := tc.(map[string]any); ok {
						part := map[string]any{"type": "tool_call"}
						if name, ok := common.ParseStr(tcMap["name"]); ok {
							part["name"] = name
						}
						if id, ok := common.ParseStr(tcMap["id"]); ok {
							part["id"] = id
						}
						if args, ok := tcMap["args"]; ok {
							part["arguments"] = args
						}
						parts = append(parts, part)
					}
				}
			}
			if len(parts) == 0 {
				parts = []map[string]any{}
			}
			otelMsg["parts"] = parts
			converted = append(converted, otelMsg)
		}
		if data, err := json.Marshal(common.ParseJSON(converted, common.MaxJSONDepth)); err == nil {
			setIfAbsent(attrs, common.GenAIInputMessages, pcommon.NewValueStr(string(data)))
		}
	}
}

// builds OTel GenAI semconv messages from indexed OpenInference span attributes
//
// When isOutput is true, each message gets a finish_reason. If finishReason is provided
// (from gen_ai.response.finish_reasons on the span), it is used directly. Otherwise
// falls back to "tool_call" if tool call parts are present, or "stop".
//
// see: https://github.com/open-telemetry/semantic-conventions/blob/main/docs/gen-ai/gen-ai-input-messages.json
// see: https://github.com/open-telemetry/semantic-conventions/blob/main/docs/gen-ai/gen-ai-output-messages.json
func convertMessages(messages map[int]map[string]any, isOutput bool, finishReason string) []map[string]any {
	keys := make([]int, 0, len(messages))
	for k := range messages {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	result := make([]map[string]any, 0, len(keys))
	for _, idx := range keys {
		msg := messages[idx]
		otelMsg := make(map[string]any)

		var role string
		if r, ok := msg["role"]; ok {
			roleStr, _ := r.(string)
			if mapped, ok := roleMap[roleStr]; ok {
				role = mapped
			} else {
				role = roleStr
			}
			otelMsg["role"] = role
		}

		var contentText string
		if content, ok := msg["content"]; ok {
			contentText, _ = content.(string)
		} else if text := extractContentBlocks(msg); text != "" {
			contentText = text
		}

		toolCalls := extractToolCallBlocks(msg)
		if len(toolCalls) == 0 {
			if tc, ok := msg["tool_calls"]; ok {
				var tcList []map[string]any
				switch v := tc.(type) {
				case string:
					_ = json.Unmarshal([]byte(v), &tcList)
				case []any:
					for _, item := range v {
						if m, ok := item.(map[string]any); ok {
							tcList = append(tcList, m)
						}
					}
				}
				toolCalls = tcList
			}
		}

		var parts []map[string]any
		if role == "tool" {
			if tcID, ok := msg["tool_call_id"]; ok {
				parts = append(parts, map[string]any{
					"type":     "tool_call_response",
					"id":       tcID,
					"response": contentText,
				})
			} else if contentText != "" {
				parts = append(parts, map[string]any{
					"type":    "text",
					"content": contentText,
				})
			}
		} else {
			if contentText != "" {
				parts = append(parts, map[string]any{
					"type":    "text",
					"content": contentText,
				})
			}
			for _, tc := range toolCalls {
				part := map[string]any{"type": "tool_call"}
				if name, ok := tc["name"]; ok {
					part["name"] = name
				}
				if id, ok := tc["id"]; ok {
					part["id"] = id
				}
				if args, ok := tc["arguments"]; ok {
					part["arguments"] = args
				}
				parts = append(parts, part)
			}
		}

		if len(parts) == 0 {
			parts = []map[string]any{}
		}
		otelMsg["parts"] = parts

		if isOutput {
			switch {
			case finishReason != "":
				otelMsg["finish_reason"] = finishReason
			case len(toolCalls) > 0:
				otelMsg["finish_reason"] = "tool_call"
			default:
				otelMsg["finish_reason"] = "stop"
			}
		}

		result = append(result, otelMsg)
	}
	return result
}

// see: https://arize-ai.github.io/openinference/spec/semantic_conventions.html#tool-calls-in-output-messages
func extractToolCallBlocks(msg map[string]any) []map[string]any {
	tcMap := make(map[int]map[string]any)
	for key, value := range msg {
		if match := toolCallKeyPattern.FindStringSubmatch(key); match != nil {
			idx, _ := strconv.Atoi(match[1])
			if tcMap[idx] == nil {
				tcMap[idx] = make(map[string]any)
			}
			switch match[2] {
			case "function.name":
				tcMap[idx]["name"] = value
			case "function.arguments":
				tcMap[idx]["arguments"] = value
			case "id":
				tcMap[idx]["id"] = value
			}
		}
	}
	if len(tcMap) == 0 {
		return nil
	}
	idxs := make([]int, 0, len(tcMap))
	for k := range tcMap {
		idxs = append(idxs, k)
	}
	sort.Ints(idxs)
	result := make([]map[string]any, 0, len(idxs))
	for _, k := range idxs {
		result = append(result, tcMap[k])
	}
	return result
}

// see: https://arize-ai.github.io/openinference/spec/semantic_conventions.html#message-content-arrays-multimodal
func extractContentBlocks(msg map[string]any) string {
	blockMap := make(map[int]map[string]any)
	for key, value := range msg {
		if match := contentBlockPattern.FindStringSubmatch(key); match != nil {
			idx, _ := strconv.Atoi(match[1])
			if blockMap[idx] == nil {
				blockMap[idx] = make(map[string]any)
			}
			blockMap[idx][match[2]] = value
		}
	}
	if len(blockMap) == 0 {
		return ""
	}
	idxs := make([]int, 0, len(blockMap))
	for k := range blockMap {
		idxs = append(idxs, k)
	}
	sort.Ints(idxs)
	var texts []string
	for _, k := range idxs {
		block := blockMap[k]
		if text, ok := block["text"].(string); ok && text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) == 1 {
		return texts[0]
	}
	if len(texts) > 1 {
		return strings.Join(texts, "\n")
	}
	return ""
}

// only sets the attribute if it doesn't already exist on the span.
func setIfAbsent(attrs pcommon.Map, key string, val pcommon.Value) {
	if _, exists := attrs.Get(key); !exists {
		val.CopyTo(attrs.PutEmpty(key))
	}
}
