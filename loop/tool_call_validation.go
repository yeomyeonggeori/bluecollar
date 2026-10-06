package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) rejectUnavailableToolCall(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if !toolAvailableForAction(request.ToolSet, actionDocument.ToolName) {
		observation := agentTurnRunner.recordUnavailableToolRequest(taskRunID, len(state.Observations)+1, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "tool_unavailable "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if observation, isRejected := unrequestedPlatformMessageSendObservation(request, actionDocument, nextObservationIDForObservations(state.Observations)); isRejected {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentExternalSendIntentRejected, marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "external_send_intent_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{}
}

func (agentTurnRunner *AgentTurnRunner) rejectMalformedToolCall(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	validationError, failureCode := malformedToolInputError(actionDocument, request.ToolSet)
	if validationError == nil {
		return toolCallActionOutcome{}
	}
	observation := newFailureObservation(nextObservationIDForObservations(state.Observations), "continue", actionDocument.ToolName, validationError.Error(), toolcontract.FailureInvalidInput, failureCode, "tool_input")
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentToolInputMalformed, marshalEventBody(observation))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "malformed_tool_input "+actionDocument.ToolName, observation.ContentText())
	result, shouldStop := stopForNoProgress(stepID)
	return noProgressToolCallActionOutcome(result, shouldStop)
}

func malformedToolInputError(actionDocument turnActionDocument, toolSet *toolcontract.ToolSet) (error, toolcontract.FailureCode) {
	if validationError := validateDescriptorToolInput(toolSet, actionDocument.ToolName, actionDocument.ToolInput); validationError != nil {
		return validationError, toolcontract.FailureCodes.InvalidInput
	}
	validationError := validateTerminalToolInput(actionDocument.ToolName, actionDocument.ToolInput, toolSet)
	if validationError != nil && isTerminalToolNameError(validationError) {
		return validationError, toolcontract.FailureCodes.ToolNameInShell
	}
	return validationError, toolcontract.FailureCodes.InvalidInput
}

func validateDescriptorToolInput(toolSet *toolcontract.ToolSet, toolName string, toolInput json.RawMessage) error {
	if toolSet == nil {
		return nil
	}
	toolDefinition, isFound := toolSet.ToolDefinition(toolName)
	if !isFound {
		return nil
	}
	_, errorValue := toolcontract.ValidateToolInput(toolDefinition.InputSchema, toolInput)
	return errorValue
}

func (agentTurnRunner *AgentTurnRunner) rejectRepeatedToolCall(taskRunID string, stepID string, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	if observation, isRepeatedRead := repeatedFileReadObservation(state.Observations, actionDocument, nextObservationIDForObservations(state.Observations)); isRepeatedRead {
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFileReadCacheHit, marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "file_read_cache_hit", observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if sentObservation, wasSent := previousSuccessfulExternalSend(state.Request.ToolSet, state.Observations, actionDocument.ToolName, actionDocument.ToolInput); wasSent {
		observation := turnObservation{
			ObservationID: nextObservationIDForObservations(state.Observations),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output:        toolcontract.ToolOutput{Content: "This task already sent to that recipient as " + sentObservation.ObservationID + ". Do not send to the same recipient again. Send to a different recipient or use that observation for completionEvidence and send a final reply."},
			Failure:       &toolcontract.ToolFailure{Kind: toolcontract.FailurePolicyBlocked, Code: toolcontract.FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This task already sent to that recipient."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentExternalSendRepeatRejected, marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "external_send_repeat_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if duplicateObservation, isDuplicate := repeatedSuccessfulToolObservation(state, actionDocument, successfulToolCalls); isDuplicate {
		observation := turnObservation{
			ObservationID: nextObservationIDForObservations(state.Observations),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output:        toolcontract.ToolOutput{Content: "This exact tool call already succeeded as " + duplicateObservation.ObservationID + ". Use that observation for completionEvidence instead of running it again."},
			Failure:       &toolcontract.ToolFailure{Kind: toolcontract.FailurePolicyBlocked, Code: toolcontract.FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: "This exact tool call already succeeded."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentDuplicateToolCallRejected, marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "duplicate_tool_call "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if refusedFailure, wasRefused := previousNonRetryableToolFailure(state.Observations, actionDocument.ToolName); wasRefused {
		observation := turnObservation{
			ObservationID: nextObservationIDForObservations(state.Observations),
			Action:        "policy",
			Tool:          strings.TrimSpace(actionDocument.ToolName),
			Output: toolcontract.ToolOutput{Content: strings.TrimSpace(actionDocument.ToolName) + " failed as " + refusedFailure.ObservationID +
				" in a way no retry can change: " + refusedFailure.Failure.UserSafeSummary +
				". Reach the goal another way, or stop and say this tool is unusable."},
			Failure: &toolcontract.ToolFailure{Kind: toolcontract.FailurePolicyBlocked, Code: toolcontract.FailureCodes.PolicyBlocked.String(), Stage: "policy", UserSafeSummary: strings.TrimSpace(actionDocument.ToolName) + " already failed in a way no retry can change."},
		}
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentNonRetryableToolRefused, marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "non_retryable_tool_refused "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	if duplicateFailure, isDuplicateFailure := previousFailedToolInput(state.Observations, actionDocument.ToolName, actionDocument.ToolInput); isDuplicateFailure {
		observation := repeatedFailedAttemptObservation(state.Request.ToolSet, len(state.Observations)+1, duplicateFailure, firstNonEmptyString(state.Request.ActiveGoal.OriginalInstruction, state.Request.Prompt))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailedFingerprintRejected, marshalEventBody(observation))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "failed_fingerprint_rejected "+actionDocument.ToolName, observation.ContentText())
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{}
}

func repeatedSuccessfulToolObservation(state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation) (turnObservation, bool) {
	observation, isDuplicate := repeatedSuccessfulCompletionCandidate(state, actionDocument, successfulToolCalls)
	if !isDuplicate || !handlesDuplicateSuccessfulToolCall(state.Request.ToolSet, actionDocument.ToolName, actionDocument.ToolInput) {
		return turnObservation{}, false
	}
	return observation, true
}

func repeatedSuccessfulCompletionCandidate(state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation) (turnObservation, bool) {
	requestExpectsSideEffect := requiredEvidenceIncludesSideEffect(state.Request.ToolSet, state.Request.RequiredEvidenceTools) ||
		outcomeContractHasSideEffectEvidence(state.Request.ToolSet, state.Request.OutcomeContract)
	if requestExpectsSideEffect && !requiredEvidenceIncludesSideEffect(state.Request.ToolSet, []string{actionDocument.ToolName}) {
		return turnObservation{}, false
	}
	toolInputKey := canonicalToolCallKey(actionDocument.ToolName, actionDocument.ToolInput)
	observation, isDuplicate := successfulToolCalls[toolInputKey]
	if !isDuplicate {
		observation, isDuplicate = previousSuccessfulToolInputObservation(state.Observations, toolInputKey)
	}
	if !isDuplicate || terminalRerunAfterWorkspaceMutation(actionDocument, state.Observations, observation) {
		return turnObservation{}, false
	}
	return observation, true
}

func previousSuccessfulToolInputObservation(observations []turnObservation, toolInputKey string) (turnObservation, bool) {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action == "continue" && !observation.Failed() && strings.TrimSpace(observation.ToolInputKey) == toolInputKey {
			return observation, true
		}
	}
	return turnObservation{}, false
}

func parseToolInputDocument(toolName string, toolInput json.RawMessage) (map[string]any, error) {
	inputDocument := map[string]any{}
	if len(toolInput) == 0 {
		return inputDocument, nil
	}
	if errorValue := json.Unmarshal(toolInput, &inputDocument); errorValue != nil {
		return nil, errors.New("tool input for " + strings.TrimSpace(toolName) + " is not valid json: " + errorValue.Error())
	}
	return inputDocument, nil
}

func canonicalToolCallKey(toolName string, toolInput json.RawMessage) string {
	return agentcontract.CanonicalToolCallKey(toolName, toolInput)
}

func canonicalToolInput(toolInput json.RawMessage) string {
	return agentcontract.CanonicalToolInput(toolInput)
}

func handlesDuplicateSuccessfulToolCall(toolSet *toolcontract.ToolSet, toolName string, toolInput json.RawMessage) bool {
	if strings.TrimSpace(toolName) == toolcontract.BashToolName {
		return true
	}
	return isOneShotCompletionEvidenceTool(toolSet, toolName)
}

func toolAvailableForAction(toolRegistry *toolcontract.ToolSet, toolName string) bool {
	if toolRegistry == nil {
		return false
	}
	return toolRegistry.IsAllowed(strings.TrimSpace(toolName))
}

func (agentTurnRunner *AgentTurnRunner) recordUnavailableToolRequest(taskRunID string, index int, toolName string, toolInput json.RawMessage, workspaceRootPath string, minimumModifiedAt time.Time) turnObservation {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		trimmedToolName = "unknown_tool"
	}
	observationID := nextObservationID(index)
	toolInputKey := canonicalToolCallKey(trimmedToolName, toolInput)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.ToolTaskEventName(trimmedToolName, agentcontract.ToolTaskEventRequestedSuffix), marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      trimmedToolName,
		"input":         json.RawMessage(toolInput),
	}))
	return agentTurnRunner.saveToolObservation(context.Background(), taskRunID, observationID, "", "", "", trimmedToolName, "", toolInput, effectiveObservationToolName(trimmedToolName, toolInput), toolInputKey, toolcontract.ToolFailureResult(toolcontract.FailurePolicyBlocked, toolcontract.FailureCodes.PolicyBlocked, "tool_availability", "tool is not allowed"), false, workspaceRootPath, minimumModifiedAt, 0)
}

func stringValue(value any) string {
	typedValue, isString := value.(string)
	if !isString {
		return ""
	}
	return typedValue
}
