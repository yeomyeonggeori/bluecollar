package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func nativeAgentActionRequest(state agentTaskState) (model.ChatCompletionRequest, bool) {
	return buildAgentActionChatCompletionRequest(buildAgentActionRequestCarryingToolResultsNatively(state), state.Observations)
}

func buildAgentActionChatCompletionRequest(structuredRequest model.StructuredResponseRequest, observations []turnObservation) (model.ChatCompletionRequest, bool) {
	messages := make([]model.ChatCompletionMessage, 0, len(structuredRequest.Messages)+2*len(observations))
	for _, message := range structuredRequest.Messages {
		messages = append(messages, model.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
			Parts:   append([]model.MessagePart{}, message.Parts...),
		})
	}
	messages = append(messages, toolCallTranscript(observations)...)
	tools, errorValue := nativeAgentActionTools(structuredRequest.StructuredOutputSchema.Document)
	if errorValue != nil || len(tools) == 0 {
		return model.ChatCompletionRequest{}, false
	}
	return model.ChatCompletionRequest{
		SchemaName:        agentActionSchemaName,
		Messages:          messages,
		Tools:             tools,
		ParallelToolCalls: true,
		GenerationOptions: structuredRequest.GenerationOptions,
	}, true
}

func toolCallTranscript(observations []turnObservation) []model.ChatCompletionMessage {
	transcript := []model.ChatCompletionMessage{}
	for _, observation := range observations {
		toolName := strings.TrimSpace(observation.Tool)
		result := toolResultForTranscript(observation)
		if observation.Action != "continue" {
			if result != "" {
				transcript = append(transcript, model.ChatCompletionMessage{Role: "system", Content: "Runtime observation (" + observation.Action + "): " + result})
			}
			continue
		}
		if toolName == "" || result == "" {
			continue
		}
		transcript = append(transcript,
			model.ChatCompletionMessage{
				Role:    "assistant",
				Content: observation.AssistantText,
				ToolCalls: []model.ChatCompletionToolCall{{
					ID:   observation.ObservationID,
					Type: "function",
					Function: model.ChatCompletionToolCallFunction{
						Name:      toolName,
						Arguments: toolCallArguments(observation.ToolInput),
					},
				}},
			},
			model.ChatCompletionMessage{
				Role:       "tool",
				ToolCallID: observation.ObservationID,
				Content:    result,
			},
		)
	}
	return transcript
}

func toolResultForTranscript(observation turnObservation) string {
	if content := strings.TrimSpace(observation.ContentText()); content != "" {
		return content
	}
	return strings.TrimSpace(observation.Summary)
}

func toolCallArguments(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return "{}"
	}
	return string(toolInput)
}

func nativeAgentActionTools(schemaDocument string) ([]model.ChatCompletionTool, error) {
	var schema struct {
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if errorValue := json.Unmarshal([]byte(schemaDocument), &schema); errorValue != nil {
		return nil, errorValue
	}
	tools := make([]model.ChatCompletionTool, 0, len(schema.OneOf))
	toolNames := map[string]bool{}
	for _, variant := range schema.OneOf {
		tool, errorValue := nativeAgentActionTool(variant)
		if errorValue != nil {
			return nil, errorValue
		}
		if toolNames[tool.Function.Name] {
			return nil, fmt.Errorf("native agent action tool %q is duplicated", tool.Function.Name)
		}
		toolNames[tool.Function.Name] = true
		tools = append(tools, tool)
	}
	return tools, nil
}

func nativeAgentActionTool(variant json.RawMessage) (model.ChatCompletionTool, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(variant, &document); errorValue != nil {
		return model.ChatCompletionTool{}, errorValue
	}
	var properties map[string]json.RawMessage
	if errorValue := json.Unmarshal(document["properties"], &properties); errorValue != nil {
		return model.ChatCompletionTool{}, errors.New("native agent action variant has no properties")
	}
	actionName, errorValue := singleSchemaEnumValue(properties["action"])
	if errorValue != nil {
		return model.ChatCompletionTool{}, errorValue
	}
	toolName := actionName
	parameters := variant
	switch {
	case actionName == "continue":
		toolName, errorValue = singleSchemaEnumValue(properties["toolName"])
		parameters = properties["toolInput"]
	case isNativeTerminalAction(actionName):
		parameters, errorValue = nativeTerminalActionParameters(document)
	default:
		return model.ChatCompletionTool{}, fmt.Errorf("native agent action %q is unsupported", actionName)
	}
	if errorValue != nil {
		return model.ChatCompletionTool{}, errorValue
	}
	if len(parameters) == 0 {
		return model.ChatCompletionTool{}, errors.New("native agent action parameters are empty")
	}
	parameters, errorValue = parametersWithReasoningSlot(parameters)
	if errorValue != nil {
		return model.ChatCompletionTool{}, errorValue
	}
	var description string
	_ = json.Unmarshal(document["description"], &description)
	return model.ChatCompletionTool{Type: "function", Function: model.ChatCompletionFunction{
		Name: toolName, Description: description, Parameters: parameters,
	}}, nil
}

const reasoningSlotName = "reasoning"

func parametersWithReasoningSlot(parameters json.RawMessage) (json.RawMessage, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(parameters, &document); errorValue != nil {
		return nil, errorValue
	}
	var properties map[string]json.RawMessage
	if json.Unmarshal(document["properties"], &properties) != nil || properties == nil {
		properties = map[string]json.RawMessage{}
	}
	properties[reasoningSlotName], _ = json.Marshal(map[string]string{
		"type":        "string",
		"description": "Think here before acting: what the last results showed and why this call is next. Carried into your next step. One or two sentences.",
	})
	propertiesDocument, errorValue := json.Marshal(properties)
	if errorValue != nil {
		return nil, errorValue
	}
	document["properties"] = propertiesDocument
	var requiredFields []string
	_ = json.Unmarshal(document["required"], &requiredFields)
	document["required"], _ = json.Marshal(append(requiredFields, reasoningSlotName))
	return json.Marshal(document)
}

func singleSchemaEnumValue(document json.RawMessage) (string, error) {
	var schema struct {
		Enum []string `json:"enum"`
	}
	if json.Unmarshal(document, &schema) != nil || len(schema.Enum) != 1 || strings.TrimSpace(schema.Enum[0]) == "" {
		return "", errors.New("native agent action discriminator must contain one value")
	}
	return schema.Enum[0], nil
}

func nativeTerminalActionParameters(document map[string]json.RawMessage) (json.RawMessage, error) {
	var properties map[string]json.RawMessage
	if errorValue := json.Unmarshal(document["properties"], &properties); errorValue != nil {
		return nil, errorValue
	}
	delete(properties, "action")
	propertiesDocument, errorValue := json.Marshal(properties)
	if errorValue != nil {
		return nil, errorValue
	}
	document["properties"] = propertiesDocument
	var requiredFields []string
	_ = json.Unmarshal(document["required"], &requiredFields)
	retainedFields := make([]string, 0, len(requiredFields))
	for _, fieldName := range requiredFields {
		if fieldName != "action" {
			retainedFields = append(retainedFields, fieldName)
		}
	}
	requiredDocument, errorValue := json.Marshal(retainedFields)
	if errorValue != nil {
		return nil, errorValue
	}
	document["required"] = requiredDocument
	return json.Marshal(document)
}

func satisfiedFinishDocument(reply string) turnActionDocument {
	goalSatisfied := true
	return turnActionDocument{
		Action:        "finish",
		Final:         true,
		Message:       reply,
		GoalStatus:    "satisfied",
		GoalSatisfied: &goalSatisfied,
	}
}

func parseNativeAgentActionResponse(response model.ChatCompletionResponse, tools []model.ChatCompletionTool) (turnActionDocument, error) {
	if response.Message.Role != "assistant" {
		return turnActionDocument{}, errors.New("native agent action chat message must be assistant")
	}
	if response.FinishReason == "stop" && len(response.Message.ToolCalls) == 0 && strings.TrimSpace(response.Message.Content) != "" {
		action := satisfiedFinishDocument(strings.TrimSpace(response.Message.Content))
		action.ModelReasoning = response.Message.Reasoning
		action.ModelReasoningField = response.Message.ReasoningField
		return action, nil
	}
	if response.FinishReason != "tool_calls" {
		return turnActionDocument{}, fmt.Errorf("native agent action chat finish reason is %q", response.FinishReason)
	}
	if len(response.Message.ToolCalls) == 0 {
		return turnActionDocument{}, errors.New("native agent action chat expected at least one tool call")
	}
	firstAction, errorValue := nativeAgentActionFromToolCall(response.Message.ToolCalls[0], tools)
	firstAction.AssistantText = firstNonEmptyString(firstAction.AssistantText, strings.TrimSpace(response.Message.Content))
	firstAction.ModelReasoning = response.Message.Reasoning
	firstAction.ModelReasoningField = response.Message.ReasoningField
	if errorValue != nil || firstAction.Action != "continue" {
		return firstAction, errorValue
	}
	firstAction.BatchedActions = batchedNativeAgentActions(response.Message.ToolCalls[1:], tools)
	return firstAction, nil
}

func batchedNativeAgentActions(toolCalls []model.ChatCompletionToolCall, tools []model.ChatCompletionTool) []turnActionDocument {
	var actions []turnActionDocument
	for _, toolCall := range toolCalls {
		action, errorValue := nativeAgentActionFromToolCall(toolCall, tools)
		if errorValue != nil || action.Action != "continue" {
			return actions
		}
		actions = append(actions, action)
	}
	return actions
}

func nativeAgentActionFromToolCall(toolCall model.ChatCompletionToolCall, tools []model.ChatCompletionTool) (turnActionDocument, error) {
	if strings.TrimSpace(toolCall.ID) == "" {
		return turnActionDocument{}, unreadableModelActionError{reason: "native agent action chat tool call ID is empty"}
	}
	if toolCall.Type != "function" || !containsNativeAgentTool(tools, toolCall.Function.Name) {
		return turnActionDocument{}, unreadableModelActionError{reason: fmt.Sprintf("native agent action chat returned unknown tool %q", toolCall.Function.Name)}
	}
	input := json.RawMessage(toolCall.Function.Arguments)
	var inputDocument map[string]json.RawMessage
	if json.Unmarshal(input, &inputDocument) != nil || inputDocument == nil {
		return turnActionDocument{}, unreadableModelActionError{reason: fmt.Sprintf("native agent action tool %q arguments must be an object, and this call sent %s", toolCall.Function.Name, truncateForLedger(string(input), 200))}
	}
	reasoning := ""
	if rawReasoning, hasReasoning := inputDocument[reasoningSlotName]; hasReasoning {
		_ = json.Unmarshal(rawReasoning, &reasoning)
		delete(inputDocument, reasoningSlotName)
	}
	strippedInput, errorValue := json.Marshal(inputDocument)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	if !isNativeTerminalAction(toolCall.Function.Name) {
		return turnActionDocument{Action: "continue", ToolName: toolCall.Function.Name, ToolInput: strippedInput, AssistantText: strings.TrimSpace(reasoning)}, nil
	}
	inputDocument["action"], _ = json.Marshal(toolCall.Function.Name)
	normalizedInput, errorValue := json.Marshal(inputDocument)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	action, errorValue := ParseAgentActionResponse(model.StructuredResponse{Content: string(normalizedInput)})
	action.AssistantText = strings.TrimSpace(reasoning)
	return action, errorValue
}

func containsNativeAgentTool(tools []model.ChatCompletionTool, toolName string) bool {
	for _, tool := range tools {
		if tool.Function.Name == toolName {
			return true
		}
	}
	return false
}

func isNativeTerminalAction(action string) bool {
	switch strings.TrimSpace(action) {
	case "reply", "fail", "set_quality_criteria":
		return true
	default:
		return false
	}
}
