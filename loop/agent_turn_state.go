package loop

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type agentTaskState struct {
	PendingBatchedActions              []turnActionDocument
	PendingBatchedToolNames            []string
	PendingBatchedToolExposure         ToolExposureEvent
	TaskRunID                          string
	Status                             agentcontract.TaskStatus
	Request                            AgentTurnRequest
	Options                            TurnOptions
	Observations                       []turnObservation
	QualityCriteria                    []qualityCriterion
	Attachments                        []toolcontract.FileAttachment
	ExecutionState                     ExecutionState
	ContextSummary                     TaskContextSummary
	IterationCount                     int
	ToolCallCount                      int
	GrantedTaskLevel                   TaskLevel
	TurnStartedAt                      time.Time
	CompletionIntentToolName           string
	ShouldRestrictNextActionToTerminal bool
	DidNudgePlan                       bool
	SystemInstruction                  string
	ActivePlanStepTitle                string
	PlanStepToolNames                  []string
	DeliveredAttachmentPaths           []string
	StepExposure                       stepToolExposure
}

type stepToolExposure struct {
	Key      string
	ToolSet  *toolcontract.ToolSet
	Exposure ToolExposureEvent
}

func (state agentTaskState) systemInstructionText() string {
	if state.SystemInstruction != "" {
		return state.SystemInstruction
	}
	return systemInstructionFor(state.Options, state.Request).Text()
}

func (state agentTaskState) didExtendBudgetOneLevel() bool {
	return state.GrantedTaskLevel != ""
}

func (state agentTaskState) budgetTaskLevel() TaskLevel {
	if state.GrantedTaskLevel != "" {
		return state.GrantedTaskLevel
	}
	return state.Request.TaskLevel
}

func buildInitialAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRunID string) agentTaskState {
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}
	normalizedOptions := normalizeTurnOptions(options)
	return agentTaskState{
		TaskRunID:         taskRunID,
		Status:            agentcontract.TaskStatusRunning,
		Request:           request,
		Options:           normalizedOptions,
		SystemInstruction: systemInstructionFor(normalizedOptions, request).Text(),
		TurnStartedAt:     request.TurnStartedAt,
		Observations:      []turnObservation{},
		Attachments:       []toolcontract.FileAttachment{},
		ToolCallCount:     0,
		IterationCount:    0,
	}
}

func agentTaskStateForTurn(request AgentTurnRequest, options TurnOptions, taskRun agentcontract.TaskRun, events []agentcontract.TaskEvent, isPausedTaskResume bool) (agentTaskState, error) {
	if !request.IsRuntimeRestartResume && !isPausedTaskResume {
		state := buildInitialAgentTaskState(request, options, taskRun.TaskRunID)
		state.Status = taskRun.Status
		return state, nil
	}
	return restoreAgentTaskState(request, options, taskRun, events)
}

func restoreAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRun agentcontract.TaskRun, events []agentcontract.TaskEvent) (agentTaskState, error) {
	if shouldCleanRestartRestoredTask(events) {
		return cleanRestartedAgentTaskState(request, options, taskRun, events), nil
	}
	state := buildInitialAgentTaskState(request, options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	state.ContextSummary = taskContextSummaryFromTaskEvents(events)
	state.Observations = observationsFromCheckpointAndTaskEvents(state.ContextSummary, events)
	if userResumeClearsInheritedFailureDebt(request, state.Observations) {
		state.Observations = observationsWithoutFailures(state.Observations)
	}
	state.Attachments = attachmentsFromObservations(state.Observations)
	state.DeliveredAttachmentPaths = deliveredAttachmentPathsFromTaskEvents(events)
	planStepSelection := planStepSelectionFromTaskEvents(events)
	state.ActivePlanStepTitle = planStepSelection.Step
	state.PlanStepToolNames = planStepSelection.ToolNames
	state.ExecutionState = executionStateFromTaskEvents(events)
	state.ToolCallCount = state.ContextSummary.CompactedToolCallCount + successfulToolCallCount(state.Observations)
	state.IterationCount = state.ContextSummary.CompactedObservationCount + len(state.Observations)
	return state, nil
}

func observationsFromCheckpointAndTaskEvents(checkpoint TaskContextSummary, events []agentcontract.TaskEvent) []turnObservation {
	if !checkpoint.accountsForTaskEvents() {
		return observationsFromTaskEvents(events)
	}
	observations := append([]turnObservation{}, checkpoint.RetainedObservations...)
	return append(observations, observationsFromTaskEvents(taskEventsExcept(events, checkpoint.AccountedTaskEventIDs))...)
}

func taskEventsExcept(events []agentcontract.TaskEvent, excludedTaskEventIDs []string) []agentcontract.TaskEvent {
	accountedTaskEventIDs := stringSet(excludedTaskEventIDs)
	remainingEvents := []agentcontract.TaskEvent{}
	for _, event := range events {
		if accountedTaskEventIDs[event.TaskEventID] {
			continue
		}
		remainingEvents = append(remainingEvents, event)
	}
	return remainingEvents
}

func userResumeClearsInheritedFailureDebt(request AgentTurnRequest, observations []turnObservation) bool {
	if !request.IsRuntimeRestartResume {
		return false
	}
	_, hasFailureDebt := activeFailureDebt(observations)
	return hasFailureDebt
}

func observationsWithoutFailures(observations []turnObservation) []turnObservation {
	retained := make([]turnObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.Failed() {
			continue
		}
		retained = append(retained, observation)
	}
	return retained
}

func shouldCleanRestartRestoredTask(events []agentcontract.TaskEvent) bool {
	lastStallIndex := -1
	for index, event := range events {
		switch event.Name {
		case agentcontract.TaskEventAgentNoProgressLoopStopped, agentcontract.TaskEventAgentNoProgressLoopPaused, agentcontract.TaskEventAgentLimitStop:
			lastStallIndex = index
		}
	}
	if lastStallIndex == -1 {
		return false
	}
	for index := lastStallIndex + 1; index < len(events); index++ {
		if events[index].Name == agentcontract.TaskEventAgentSteerReceived || events[index].Name == agentcontract.TaskEventTaskSteerRequested {
			return true
		}
	}
	return false
}

func cleanRestartedAgentTaskState(request AgentTurnRequest, options TurnOptions, taskRun agentcontract.TaskRun, events []agentcontract.TaskEvent) agentTaskState {
	state := buildInitialAgentTaskState(scrubRestoredGoalContext(request), options, taskRun.TaskRunID)
	state.Status = taskRun.Status
	durableObservations := durableDeliveryObservations(events)
	state.Observations = append(durableObservations, regroundingObservation(len(durableObservations)+1, producedSourcePaths(events)))
	state.Attachments = attachmentsFromObservations(state.Observations)
	state.DeliveredAttachmentPaths = deliveredAttachmentPathsFromTaskEvents(events)
	return state
}

func scrubRestoredGoalContext(request AgentTurnRequest) AgentTurnRequest {
	request.ActiveGoal.KnownContext = []string{"The prior attempt on this task stalled and its working notes were cleared. Ignore the earlier trajectory and earlier tool outputs; re-ground from the current workspace state: read the deliverable source and any build or review output already saved on disk, and continue improving that same source in place rather than recreating it from scratch."}
	return request
}

func durableDeliveryObservations(events []agentcontract.TaskEvent) []turnObservation {
	durable := []turnObservation{}
	for _, observation := range observationsFromTaskEvents(events) {
		if observation.Failed() {
			continue
		}
		if isDurableDeliveryObservation(observation) {
			durable = append(durable, observation)
		}
	}
	return durable
}

func producedSourcePaths(events []agentcontract.TaskEvent) []string {
	const maxSourcePaths = 8
	seen := map[string]bool{}
	paths := []string{}
	addPath := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] || len(paths) >= maxSourcePaths {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, observation := range observationsFromTaskEvents(events) {
		if observation.Failed() {
			continue
		}
		switch strings.TrimSpace(observation.Tool) {
		case toolcontract.WriteToolName:
			var result struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(observation.StructuredOutput(), &result) == nil {
				addPath(result.Path)
			}
		case toolcontract.EditToolName:
			var result struct {
				EditedFiles []string `json:"editedFiles"`
			}
			if json.Unmarshal(observation.StructuredOutput(), &result) == nil {
				for _, path := range result.EditedFiles {
					addPath(path)
				}
			}
		}
	}
	return paths
}

func isDurableDeliveryObservation(observation turnObservation) bool {
	return len(observation.Attachments) > 0
}

func regroundingObservation(index int, sourcePaths []string) turnObservation {
	message := "The previous attempt on this task stalled without finishing, and its working notes were cleared to avoid repeating the same mistakes. Your file edits on disk are preserved. Re-ground before acting: read the deliverable source you were producing on disk and any build or review output saved beside it, then continue that same workflow — improve the existing source in place rather than recreating it from scratch. If a build, review, or quality score already exists on disk, treat it as your target and iterate toward it. Do not trust earlier tool outputs; verify the current state first."
	if len(sourcePaths) > 0 {
		message += " The source files you were producing are: " + strings.Join(sourcePaths, ", ") + ". Read these first and keep improving them in place."
	}
	observation := newContentObservation(nextObservationID(index), "policy", "", marshalEventBody(map[string]string{
		"regrounded": "true",
		"directive":  message,
	}))
	observation.Summary = message
	return observation
}

func applyToolResult(state agentTaskState, invocation toolcontract.ToolInvocation, result toolcontract.ToolResult) agentTaskState {
	result = normalizeToolFailureResult(invocation.ToolName, result)
	toolInputKey := canonicalToolCallKey(invocation.ToolName, invocation.Input)
	observation := turnObservation{
		ObservationID:   nextObservationIDForObservations(state.Observations),
		Action:          "continue",
		Tool:            strings.TrimSpace(invocation.ToolName),
		Output:          result.Output,
		Failure:         result.Failure,
		Summary:         modelVisibleToolResultSummary(context.Background(), nil, invocation.ToolName, turnObservation{Tool: invocation.ToolName, Output: result.Output, Failure: result.Failure, Attachments: result.Attachments}),
		ToolInputKey:    toolInputKey,
		RecoveryActions: append([]toolcontract.RecoveryAction{}, result.RecoveryActions...),
	}
	if observation.Failed() {
		observation.AttemptFingerprint = attemptFingerprint(toolInputKey, observation.FailureCode())
	}
	if !result.Failed() {
		observation.Attachments = append([]toolcontract.FileAttachment{}, result.Attachments...)
		observation.ReplyNotes = append([]string{}, result.ReplyNotes...)
		state.Attachments = appendObservationAttachments(state.Attachments, observation)
	}
	state.Observations = append(state.Observations, observation)
	return state
}

func isToolResultTaskEvent(event agentcontract.TaskEvent) bool {
	_, isToolResult := agentcontract.ToolTaskEventToolName(event.Name, agentcontract.ToolTaskEventResultSuffix)
	return isToolResult
}

func observationsFromTaskEvents(events []agentcontract.TaskEvent) []turnObservation {
	observations := []turnObservation{}
	unanswered := map[string]requestedToolCall{}
	for _, event := range events {
		if requestedCall, isRequest := requestedToolCallFromTaskEvent(event); isRequest {
			unanswered[requestedCall.ObservationID] = requestedCall
			continue
		}
		if observation, isReplyReceipt := replyReceiptObservationFromTaskEvent(event); isReplyReceipt {
			observations = append(observations, observation)
			continue
		}
		if !isToolResultTaskEvent(event) {
			continue
		}
		observation, errorValue := decodeTurnObservation([]byte(event.Body))
		if errorValue != nil || strings.TrimSpace(observation.ObservationID) == "" {
			continue
		}
		delete(unanswered, observation.ObservationID)
		observation.Tool = toolcontract.CanonicalToolName(observation.Tool)
		observation.ToolInputKey = canonicalizePersistedToolCallKey(observation.ToolInputKey)
		observation.AttemptFingerprint = canonicalizePersistedToolCallKey(observation.AttemptFingerprint)
		observation.RecoveryAttemptKey = canonicalizePersistedToolCallKey(observation.RecoveryAttemptKey)
		if !isApprovalRequiredObservation(observation) {
			observations = append(observations, observation)
		}
	}
	return append(observations, interruptedCallObservations(unanswered)...)
}

func canonicalizePersistedToolCallKey(toolCallKey string) string {
	toolName, toolInput, hasDelimiter := strings.Cut(toolCallKey, "\x00")
	if !hasDelimiter {
		return toolCallKey
	}
	return toolcontract.CanonicalToolName(toolName) + "\x00" + toolInput
}

type requestedToolCall struct {
	ObservationID string          `json:"observationID"`
	ToolName      string          `json:"toolName"`
	Input         json.RawMessage `json:"input"`
}

func requestedToolCallFromTaskEvent(event agentcontract.TaskEvent) (requestedToolCall, bool) {
	if _, isToolRequest := agentcontract.ToolTaskEventToolName(event.Name, agentcontract.ToolTaskEventRequestedSuffix); !isToolRequest {
		return requestedToolCall{}, false
	}
	requestedCall := requestedToolCall{}
	if json.Unmarshal([]byte(event.Body), &requestedCall) != nil || strings.TrimSpace(requestedCall.ObservationID) == "" {
		return requestedToolCall{}, false
	}
	return requestedCall, true
}

func interruptedCallObservations(unanswered map[string]requestedToolCall) []turnObservation {
	observationIDs := make([]string, 0, len(unanswered))
	for observationID := range unanswered {
		observationIDs = append(observationIDs, observationID)
	}
	sort.Strings(observationIDs)
	observations := make([]turnObservation, 0, len(observationIDs))
	for _, observationID := range observationIDs {
		requestedCall := unanswered[observationID]
		observation := newFailureObservation(observationID, "continue", toolcontract.CanonicalToolName(requestedCall.ToolName),
			"This call was started and the runtime stopped before its result was recorded, so whether it took effect is unknown. Check the current state before running it again.",
			toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "interrupted")
		observation.ToolInput = append(json.RawMessage{}, requestedCall.Input...)
		observations = append(observations, observation)
	}
	return observations
}

func attachmentsFromObservations(observations []turnObservation) []toolcontract.FileAttachment {
	attachments := []toolcontract.FileAttachment{}
	for _, observation := range observations {
		attachments = appendObservationAttachments(attachments, observation)
	}
	return attachments
}

func successfulToolCallCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		if isToolCallObservation(observation) && !observation.Failed() {
			count++
		}
	}
	return count
}

func isToolCallObservation(observation turnObservation) bool {
	action := strings.TrimSpace(observation.Action)
	return action == "continue"
}

type legacyTurnObservation struct {
	ObservationID        string                        `json:"observationID"`
	Action               string                        `json:"action"`
	Tool                 string                        `json:"tool,omitempty"`
	Content              string                        `json:"content"`
	Summary              string                        `json:"summary,omitempty"`
	IsError              bool                          `json:"isError"`
	Message              string                        `json:"message,omitempty"`
	ErrorCode            string                        `json:"errorCode,omitempty"`
	FailureStage         string                        `json:"failureStage,omitempty"`
	Retryable            bool                          `json:"retryable,omitempty"`
	SafeRetry            bool                          `json:"safeRetry,omitempty"`
	ToolInputKey         string                        `json:"toolInputKey,omitempty"`
	AttemptFingerprint   string                        `json:"attemptFingerprint,omitempty"`
	RecoveryAttemptKey   string                        `json:"recoveryAttemptKey,omitempty"`
	RecoveryStep         string                        `json:"recoveryStep,omitempty"`
	RecoveryAttemptSpent bool                          `json:"recoveryAttemptSpent,omitempty"`
	RecoveryPacket       *RecoveryPacket               `json:"recoveryPacket,omitempty"`
	Attachments          []toolcontract.FileAttachment `json:"attachments,omitempty"`
	RecoveryActions      []toolcontract.RecoveryAction `json:"recoveryActions,omitempty"`
}

func decodeTurnObservation(document []byte) (turnObservation, error) {
	var observation turnObservation
	if errorValue := json.Unmarshal(document, &observation); errorValue != nil {
		return turnObservation{}, errorValue
	}
	if observation.Output.Content != "" || len(observation.Output.Data) > 0 || observation.Failure != nil {
		return observation, nil
	}
	var legacyObservation legacyTurnObservation
	if errorValue := json.Unmarshal(document, &legacyObservation); errorValue != nil {
		return turnObservation{}, errorValue
	}
	return legacyObservation.toTurnObservation(), nil
}

func (legacyObservation legacyTurnObservation) toTurnObservation() turnObservation {
	action := legacyObservation.Action
	observation := turnObservation{
		ObservationID:        legacyObservation.ObservationID,
		Action:               action,
		Tool:                 legacyObservation.Tool,
		Output:               toolcontract.ToolOutput{Content: legacyObservation.Content},
		Summary:              legacyObservation.Summary,
		ToolInputKey:         legacyObservation.ToolInputKey,
		AttemptFingerprint:   legacyObservation.AttemptFingerprint,
		RecoveryAttemptKey:   legacyObservation.RecoveryAttemptKey,
		RecoveryStep:         legacyObservation.RecoveryStep,
		RecoveryAttemptSpent: legacyObservation.RecoveryAttemptSpent,
		RecoveryPacket:       legacyObservation.RecoveryPacket,
		Attachments:          append([]toolcontract.FileAttachment{}, legacyObservation.Attachments...),
		RecoveryActions:      append([]toolcontract.RecoveryAction{}, legacyObservation.RecoveryActions...),
	}
	if legacyObservation.IsError {
		observation.Failure = &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureUnknown,
			Code:            toolcontract.CanonicalFailureCode(toolcontract.FailureCode(legacyObservation.ErrorCode)),
			Stage:           strings.TrimSpace(legacyObservation.FailureStage),
			UserSafeSummary: firstNonEmptyString(strings.TrimSpace(legacyObservation.Message), strings.TrimSpace(legacyObservation.Content)),
			Retryable:       legacyObservation.Retryable,
			SafeRetry:       legacyObservation.SafeRetry,
		}
	}
	return observation
}
