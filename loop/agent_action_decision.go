package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const maximumAgentActionCorrectionCount = 2

func DecideAgentAction(ctx context.Context, languageModel model.LanguageModelProvider, state agentTaskState) (turnActionDocument, error) {
	if chatCompleter, isAvailable := model.ResolveTextChatCompleter(languageModel); isAvailable {
		if chatRequest, isRepresentable := nativeAgentActionRequest(state); isRepresentable {
			return decideAgentActionWithChat(ctx, chatCompleter, chatRequest, state)
		}
	}
	structuredRequest := BuildAgentActionRequest(state)
	structuredResponse, errorValue := languageModel.GenerateStructuredResponse(ctx, structuredRequest)
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return ParseAgentActionResponse(structuredResponse)
}

func decideAgentActionWithChat(ctx context.Context, chatCompleter model.ChatCompleter, request model.ChatCompletionRequest, state agentTaskState) (turnActionDocument, error) {
	currentRequest := request
	for correctionCount := 0; ; correctionCount++ {
		response, errorValue := chatCompleter.GenerateChatCompletion(ctx, currentRequest)
		if errorValue == nil {
			action, parseError := parseNativeAgentActionResponse(response, currentRequest.Tools)
			if parseError == nil {
				return action, nil
			}
			retryRequest, canRetry := correctedAgentActionRequest(currentRequest, nativeActionParseCorrection(parseError), state, correctionCount)
			if !canRetry {
				return turnActionDocument{}, parseError
			}
			currentRequest = retryRequest
			continue
		}
		if errors.Is(errorValue, context.Canceled) || errors.Is(errorValue, context.DeadlineExceeded) || ctx.Err() != nil {
			return turnActionDocument{}, errorValue
		}
		correction, isCorrectable := model.StructuredOutputCorrectionFromError(errorValue)
		if !isCorrectable {
			return turnActionDocument{}, errorValue
		}
		retryRequest, canRetry := correctedAgentActionRequest(currentRequest, correction, state, correctionCount)
		if !canRetry {
			return turnActionDocument{}, errorValue
		}
		currentRequest = retryRequest
	}
}

func correctedAgentActionRequest(request model.ChatCompletionRequest, correction model.StructuredOutputCorrection, state agentTaskState, correctionCount int) (model.ChatCompletionRequest, bool) {
	if correctionCount >= maximumAgentActionCorrectionCount {
		return model.ChatCompletionRequest{}, false
	}
	return retryAgentActionChatCompletionRequest(request, correction, state)
}

func retryAgentActionChatCompletionRequest(request model.ChatCompletionRequest, correction model.StructuredOutputCorrection, state agentTaskState) (model.ChatCompletionRequest, bool) {
	retryRequest := request
	retryRequest.Messages = append([]model.ChatCompletionMessage{}, request.Messages...)
	retryRequest.Messages = append(retryRequest.Messages, model.ChatCompletionMessage{
		Role:    "system",
		Content: agentActionCorrectionMessage(correction),
	})
	toolName := strings.TrimSpace(correction.Diagnostic.ToolName)
	if toolName == "" {
		if correction.Diagnostic.Category != model.StructuredOutputDiagnosticFinishReason ||
			correction.Diagnostic.FinishReason != model.StructuredOutputDiagnosticFinishStop {
			return retryRequest, true
		}
		toolName = firstPendingActionToolName(state)
		if toolName == "" {
			return retryRequest, true
		}
	}
	return restrictAgentActionChatCompletionRequest(retryRequest, toolName)
}

func restrictAgentActionChatCompletionRequest(request model.ChatCompletionRequest, toolName string) (model.ChatCompletionRequest, bool) {
	for _, tool := range request.Tools {
		if tool.Function.Name != toolName {
			continue
		}
		request.Tools = []model.ChatCompletionTool{tool}
		request.ToolChoice = json.RawMessage(`"required"`)
		request.ParallelToolCalls = false
		return request, true
	}
	return model.ChatCompletionRequest{}, false
}

func firstPendingActionToolName(state agentTaskState) string {
	if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
		return ""
	}
	return firstPendingRequiredToolName(
		state.Request.ContractToolWorkingSet.RequiredNextTools,
		state.Observations,
	)
}

func nativeActionParseCorrection(parseError error) model.StructuredOutputCorrection {
	return model.StructuredOutputCorrection{
		Diagnostic: model.StructuredOutputDiagnostic{
			Category:         model.StructuredOutputDiagnosticSchemaValidation,
			ValidationIssues: actionParseValidationIssues(parseError),
		},
	}
}

func actionParseValidationIssues(parseError error) []model.StructuredOutputValidationIssue {
	var wrongTypedFields wrongTypedActionFieldError
	if !errors.As(parseError, &wrongTypedFields) {
		return []model.StructuredOutputValidationIssue{{FieldPath: parseError.Error()}}
	}
	issues := make([]model.StructuredOutputValidationIssue, 0, len(wrongTypedFields.fieldNames))
	for _, fieldName := range wrongTypedFields.fieldNames {
		issues = append(issues, model.StructuredOutputValidationIssue{FieldPath: fieldName, Code: model.StructuredOutputValidationType})
	}
	return issues
}

func agentActionCorrectionMessage(correction model.StructuredOutputCorrection) string {
	diagnostic := correction.Diagnostic
	messageParts := []string{
		"The previous native action response was invalid.",
		"Return exactly one valid tool call.",
		"Diagnostic category: " + string(diagnostic.Category) + ".",
	}
	if diagnostic.ToolName != "" {
		messageParts = append(messageParts, "Expected tool: "+diagnostic.ToolName+".")
	}
	if diagnostic.FinishReason != "" {
		messageParts = append(messageParts, "Observed finish reason: "+string(diagnostic.FinishReason)+".")
	}
	for _, issue := range diagnostic.ValidationIssues {
		messageParts = append(messageParts, "Validation issue: "+issue.FieldPath+" ("+string(issue.Code)+").")
	}
	return strings.Join(messageParts, " ")
}
