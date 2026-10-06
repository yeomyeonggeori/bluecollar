package loop

import "github.com/yeomyeonggeori/blueprotocol/model"

func (agentTurnRunner *AgentTurnRunner) estimateActionPromptTokenCount(state agentTaskState) int {
	if _, isAvailable := model.ResolveTextChatCompleter(agentTurnRunner.languageModel); isAvailable {
		if request, isRepresentable := nativeAgentActionRequest(state); isRepresentable {
			return estimateChatCompletionTokenCount(request)
		}
	}
	return estimatePromptTokenCount(BuildAgentActionRequest(state).Messages)
}

func estimateChatCompletionTokenCount(request model.ChatCompletionRequest) int {
	messages := make([]model.Message, 0, len(request.Messages))
	metadataBytes := 0
	for _, message := range request.Messages {
		messages = append(messages, model.Message{Role: message.Role, Content: message.Content, Parts: message.Parts})
		metadataBytes += len(message.ToolCallID) + len(message.Reasoning) + len(message.ReasoningField)
		for _, call := range message.ToolCalls {
			metadataBytes += len(call.ID) + len(call.Type) + len(call.Function.Name) + len(call.Function.Arguments)
		}
	}
	for _, tool := range request.Tools {
		metadataBytes += len(tool.Type) + len(tool.Function.Name) + len(tool.Function.Description) + len(tool.Function.Parameters)
	}
	return estimatePromptTokenCount(messages) + (metadataBytes+charactersPerToken-1)/charactersPerToken
}

func estimatePromptTokenCount(messages []model.Message) int {
	byteCount := 0
	for _, message := range messages {
		byteCount += len(message.Role) + len(message.Content)
		for _, part := range message.Parts {
			byteCount += len(part.Type) + len(part.Text) + len(part.MimeType) + len(part.DataBase64)
		}
	}
	return (byteCount + charactersPerToken - 1) / charactersPerToken
}

func compactionTriggerTokenThreshold(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		return defaultCompactionTriggerTokens
	}
	threshold := contextWindowTokens * conversationShareOfContextPercent / 100
	if threshold <= 0 {
		return defaultCompactionTriggerTokens
	}
	return threshold
}
