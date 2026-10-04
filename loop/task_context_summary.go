package loop

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const defaultCompactionTriggerTokens = 96000
const taskContextCompactionRecentObservationCount = 10
const taskContextCompactionMinimumNewObservations = 6
const taskContextCompactionMinimumNewCharacters = 20000

type TaskContextSummary struct {
	TaskContextSummaryContent
	ObservationID                     string   `json:"observationID,omitempty"`
	CompactedThroughObservationID     string   `json:"compactedThroughObservationID,omitempty"`
	CompactedObservationIDs           []string `json:"compactedObservationIDs,omitempty"`
	SavedTranscript                   string   `json:"savedTranscript,omitempty"`
	OmittedToolObservationIDs         []string `json:"omittedToolObservationIDs,omitempty"`
	SummarizedAssistantObservationIDs []string `json:"summarizedAssistantObservationIDs,omitempty"`
	CompactedConversationMessageKeys  []string `json:"compactedConversationMessageKeys,omitempty"`

	AccountedTaskEventIDs     []string          `json:"accountedTaskEventIDs,omitempty"`
	RetainedObservations      []turnObservation `json:"retainedObservations,omitempty"`
	CompactedObservationCount int               `json:"compactedObservationCount,omitempty"`
	CompactedToolCallCount    int               `json:"compactedToolCallCount,omitempty"`
}

type TaskContextSummaryContent struct {
	Goal                    string                       `json:"goal"`
	Context                 string                       `json:"context"`
	Constraints             []string                     `json:"constraints"`
	CompletedSteps          []string                     `json:"completedSteps"`
	PendingSteps            []string                     `json:"pendingSteps"`
	Artifacts               []string                     `json:"artifacts"`
	KeyDecisions            []string                     `json:"keyDecisions"`
	ExhaustedRecoveryRoutes []string                     `json:"exhaustedRecoveryRoutes"`
	ActiveFailureDebt       []string                     `json:"activeFailureDebt"`
	OpenQuestions           []string                     `json:"openQuestions"`
	NextPlan                []string                     `json:"nextPlan"`
	Evidence                []TaskContextSummaryEvidence `json:"evidence"`
}

type TaskContextSummaryEvidence struct {
	Fact           string   `json:"fact"`
	ObservationIDs []string `json:"observationIDs"`
}

func (summary TaskContextSummary) accountsForTaskEvents() bool {
	return len(summary.AccountedTaskEventIDs) > 0
}

func taskContextSummaryFromTaskEvents(events []agentcontract.TaskEvent) TaskContextSummary {
	for index := len(events) - 1; index >= 0; index-- {
		if strings.TrimSpace(events[index].Name) != agentcontract.TaskEventAgentContextSummary {
			continue
		}
		var summary TaskContextSummary
		if json.Unmarshal([]byte(events[index].Body), &summary) == nil {
			return normalizeTaskContextSummary(summary)
		}
	}
	return TaskContextSummary{}
}

func (agentTurnRunner *AgentTurnRunner) promptStateForAction(ctx context.Context, taskRunID string, state agentTaskState) agentTaskState {
	taskEvents := agentTurnRunner.taskRunService.ListTaskEvent(taskRunID)
	currentSummary := latestTaskContextSummary(state.ContextSummary, taskEvents)
	pinnedObservationIDs := pinnedPromptObservationIDs(state.Observations, taskEvents)
	promptState := stateWithContextSummary(state, currentSummary, pinnedObservationIDs)
	estimatedTokenCount := agentTurnRunner.estimateActionPromptTokenCount(promptState)
	if estimatedTokenCount <= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) {
		return promptState
	}
	if agentTurnRunner.decisionModel == nil {
		promptState.Observations, estimatedTokenCount = agentTurnRunner.promptObservationsWithLongToolResultsPruned(taskRunID, promptState, promptState.Observations, pinnedObservationIDs, estimatedTokenCount)
		if estimatedTokenCount <= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) {
			return promptState
		}
	}
	plan, shouldCompact := buildTaskContextCompactionPlan(promptState.Observations, currentSummary, pinnedObservationIDs)
	shouldCompact = shouldCompact || hasCompactableConversation(promptState)
	plan.AttemptKey = contextCompactionBoundary(promptState)
	if !shouldCompact || compactionAlreadyFreedNothing(taskEvents, plan.AttemptKey) {
		return promptState
	}
	summary, ok := agentTurnRunner.generateTaskContextSummary(ctx, promptState, currentSummary)
	if !ok {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextCompactionFreedNothing, marshalEventBody(map[string]string{"compactedThroughObservationID": plan.AttemptKey, "stage": "summary", "error": "no valid task-state summary was produced"}))
		return promptState
	}
	return agentTurnRunner.selectContextForAction(ctx, taskRunID, state, promptState, currentSummary, summary, plan, pinnedObservationIDs, taskEvents)
}

func (agentTurnRunner *AgentTurnRunner) saveCompactedTranscript(ctx context.Context, taskRunID string, workspaceRootPath string, plan taskContextCompactionPlan) string {
	spillRef := agentTurnRunner.spillToolResult(ctx, taskRunID, "context-summary-"+firstNonEmptyString(plan.AttemptKey, plan.CompactedThroughObservationID), compactedTranscriptName, workspaceRootPath, compactedPlanTranscript(plan))
	if !spillRef.isUsable() {
		return ""
	}
	return compactedTranscriptAdvice(spillRef)
}

const compactedTranscriptName = "compacted_steps"

func compactedPlanTranscript(plan taskContextCompactionPlan) string {
	transcript := compactedTranscript(plan.CompactableObservations)
	if plan.PreviousTranscript != "" {
		transcript += marshalEventBody(compactedStep{Action: "prior_transcript", Content: plan.PreviousTranscript}) + "\n"
	}
	for _, message := range plan.ConversationMessages {
		transcript += marshalEventBody(compactedStep{Action: "conversation_message", Content: marshalEventBody(message)}) + "\n"
	}
	return transcript
}

type compactedStep struct {
	ObservationID string           `json:"observationID"`
	Action        string           `json:"action"`
	Tool          string           `json:"tool,omitempty"`
	ToolInput     json.RawMessage  `json:"toolInput,omitempty"`
	AssistantText string           `json:"assistantText,omitempty"`
	Content       string           `json:"content,omitempty"`
	FailureCode   string           `json:"failureCode,omitempty"`
	Observation   *turnObservation `json:"observation,omitempty"`
}

func compactedTranscript(observations []turnObservation) string {
	lines := make([]string, 0, len(observations))
	for _, observation := range observations {
		lines = append(lines, marshalEventBody(compactedStep{
			ObservationID: observation.ObservationID,
			Action:        observation.Action,
			Tool:          observation.Tool,
			ToolInput:     observation.ToolInput,
			AssistantText: observation.AssistantText,
			Content:       observation.ContentText(),
			FailureCode:   observation.FailureCode(),
			Observation:   &observation,
		}))
	}
	return strings.Join(lines, "\n") + "\n"
}

func compactedTranscriptAdvice(spillRef ToolResultSpillRef) string {
	advice := "Every step this summary replaced was saved whole, one JSON line per step, at " + strings.TrimSpace(spillRef.Locator)
	if hint := strings.TrimSpace(spillRef.RetrievalHint); hint != "" {
		advice += ". " + hint
	}
	return advice + " Look there when a detail the summary dropped matters, rather than redoing the step."
}

// Once a summary of the same observations came back no smaller than what it replaced,
// asking for it again every step buys the same nothing and pays the summarizer for it.
func compactionAlreadyFreedNothing(events []agentcontract.TaskEvent, compactedThroughObservationID string) bool {
	trimmedObservationID := strings.TrimSpace(compactedThroughObservationID)
	for _, taskEvent := range events {
		if strings.TrimSpace(taskEvent.Name) != agentcontract.TaskEventAgentContextCompactionFreedNothing {
			continue
		}
		attempt := struct {
			CompactedThroughObservationID string `json:"compactedThroughObservationID"`
		}{}
		if json.Unmarshal([]byte(taskEvent.Body), &attempt) == nil && strings.TrimSpace(attempt.CompactedThroughObservationID) == trimmedObservationID {
			return true
		}
	}
	return false
}

// A pass that does not actually shrink the prompt is discarded, so a turn never reports
// progress it did not make and never sends a summarizer a projection it did not improve.
func (agentTurnRunner *AgentTurnRunner) promptObservationsWithLongToolResultsPruned(taskRunID string, state agentTaskState, promptObservations []turnObservation, pinnedObservationIDs map[string]bool, estimatedTokenCount int) ([]turnObservation, int) {
	prunedObservations, didPrune := observationsWithLongToolResultsPruned(promptObservations, pinnedObservationIDs)
	if !didPrune {
		return promptObservations, estimatedTokenCount
	}
	prunedTokenCount := agentTurnRunner.estimateActionPromptTokenCount(withPromptObservations(state, prunedObservations))
	if prunedTokenCount >= estimatedTokenCount {
		return promptObservations, estimatedTokenCount
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentToolResultsPruned, marshalEventBody(map[string]any{
		"estimatedTokensBefore": estimatedTokenCount,
		"estimatedTokensAfter":  prunedTokenCount,
	}))
	return prunedObservations, prunedTokenCount
}

func summaryAccountingForCompactedObservations(summary TaskContextSummary, previousSummary TaskContextSummary, compactedObservations []turnObservation, plan taskContextCompactionPlan, events []agentcontract.TaskEvent) TaskContextSummary {
	taskEventIDByObservationID := taskEventIDByObservationID(events)
	newlyCompactedObservations := observationsNotYetCompacted(plan.CompactableObservations, taskEventIDByObservationID, previouslyCompactedTaskEventIDs(previousSummary, taskEventIDByObservationID))

	summary.RetainedObservations = observationsExcept(compactedObservations, summary.ObservationID)
	summary.CompactedObservationCount = previousSummary.CompactedObservationCount + countRemovedObservations(newlyCompactedObservations, compactedObservations)
	summary.CompactedToolCallCount = previousSummary.CompactedToolCallCount + countRemovedToolCalls(newlyCompactedObservations, compactedObservations)
	summary.AccountedTaskEventIDs = appendMissingStrings(previousSummary.AccountedTaskEventIDs,
		taskEventIDsOf(taskEventIDByObservationID, append(observationIDsOf(summary.RetainedObservations), plan.CompactedObservationIDs...)))
	return summary
}

func previouslyCompactedTaskEventIDs(previousSummary TaskContextSummary, taskEventIDByObservationID map[string]string) map[string]bool {
	compactedTaskEventIDs := stringSet(previousSummary.AccountedTaskEventIDs)
	for _, observationID := range observationIDsOf(previousSummary.RetainedObservations) {
		delete(compactedTaskEventIDs, taskEventIDByObservationID[strings.TrimSpace(observationID)])
	}
	return compactedTaskEventIDs
}

func observationsNotYetCompacted(observations []turnObservation, taskEventIDByObservationID map[string]string, compactedTaskEventIDs map[string]bool) []turnObservation {
	pendingObservations := []turnObservation{}
	for _, observation := range observations {
		if compactedTaskEventIDs[taskEventIDByObservationID[strings.TrimSpace(observation.ObservationID)]] {
			continue
		}
		pendingObservations = append(pendingObservations, observation)
	}
	return pendingObservations
}

func observationsExcept(observations []turnObservation, excludedObservationID string) []turnObservation {
	keptObservations := []turnObservation{}
	for _, observation := range observations {
		if strings.TrimSpace(observation.ObservationID) == strings.TrimSpace(excludedObservationID) {
			continue
		}
		keptObservations = append(keptObservations, observation)
	}
	return keptObservations
}

func observationIDsOf(observations []turnObservation) []string {
	observationIDs := []string{}
	for _, observation := range observations {
		observationIDs = append(observationIDs, observation.ObservationID)
	}
	return observationIDs
}

func taskEventIDByObservationID(events []agentcontract.TaskEvent) map[string]string {
	taskEventIDByObservationID := map[string]string{}
	for _, event := range events {
		if !isToolResultTaskEvent(event) {
			continue
		}
		observation, errorValue := decodeTurnObservation([]byte(event.Body))
		if errorValue != nil {
			continue
		}
		if observationID := strings.TrimSpace(observation.ObservationID); observationID != "" {
			taskEventIDByObservationID[observationID] = event.TaskEventID
		}
	}
	return taskEventIDByObservationID
}

func taskEventIDsOf(taskEventIDByObservationID map[string]string, observationIDs []string) []string {
	taskEventIDs := []string{}
	for _, observationID := range observationIDs {
		if taskEventID := taskEventIDByObservationID[strings.TrimSpace(observationID)]; taskEventID != "" {
			taskEventIDs = append(taskEventIDs, taskEventID)
		}
	}
	return taskEventIDs
}

func appendMissingStrings(values []string, additionalValues []string) []string {
	presentValues := stringSet(values)
	combinedValues := append([]string{}, values...)
	for _, additionalValue := range additionalValues {
		if presentValues[additionalValue] {
			continue
		}
		presentValues[additionalValue] = true
		combinedValues = append(combinedValues, additionalValue)
	}
	return combinedValues
}

func latestTaskContextSummary(fallback TaskContextSummary, events []agentcontract.TaskEvent) TaskContextSummary {
	summary := taskContextSummaryFromTaskEvents(events)
	if strings.TrimSpace(summary.ObservationID) != "" {
		return summary
	}
	return fallback
}

func withPromptObservations(state agentTaskState, observations []turnObservation) agentTaskState {
	state.Observations = append([]turnObservation{}, observations...)
	return state
}

type taskContextCompactionPlan struct {
	AttemptKey                    string
	ConversationMessages          []agentcontract.VisibleContextMessage
	PreviousTranscript            string
	CompactableObservations       []turnObservation
	CompactedObservationIDs       []string
	CompactedThroughObservationID string
}

func buildTaskContextCompactionPlan(observations []turnObservation, summary TaskContextSummary, pinnedObservationIDs map[string]bool) (taskContextCompactionPlan, bool) {
	if !hasEnoughNewObservationsForCompaction(observations, summary.CompactedThroughObservationID) {
		return taskContextCompactionPlan{}, false
	}
	cutoffIndex := len(observations) - taskContextCompactionRecentObservationCount
	if cutoffIndex <= 0 {
		return taskContextCompactionPlan{}, false
	}
	plan := taskContextCompactionPlan{}
	for _, observation := range observations[:cutoffIndex] {
		if pinnedObservationIDs[strings.TrimSpace(observation.ObservationID)] || observation.Action == "context_summary" {
			continue
		}
		plan.CompactableObservations = append(plan.CompactableObservations, observation)
		plan.CompactedObservationIDs = append(plan.CompactedObservationIDs, observation.ObservationID)
		plan.CompactedThroughObservationID = observation.ObservationID
	}
	return plan, len(plan.CompactableObservations) > 0
}

func hasEnoughNewObservationsForCompaction(observations []turnObservation, compactedThroughObservationID string) bool {
	newObservations := observationsAfterObservationID(observations, compactedThroughObservationID)
	if len(newObservations) >= taskContextCompactionMinimumNewObservations {
		return true
	}
	return observationsCharacterCount(newObservations) >= taskContextCompactionMinimumNewCharacters
}

func observationsAfterObservationID(observations []turnObservation, observationID string) []turnObservation {
	trimmedObservationID := strings.TrimSpace(observationID)
	if trimmedObservationID == "" {
		return observations
	}
	for index, observation := range observations {
		if strings.TrimSpace(observation.ObservationID) == trimmedObservationID {
			return observations[index+1:]
		}
	}
	return observations
}

func observationsCharacterCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		count += len(observation.ContentText()) + len(observation.Summary)
	}
	return count
}

func promptVisibleObservations(observations []turnObservation, summary TaskContextSummary, pinnedObservationIDs map[string]bool) []turnObservation {
	if strings.TrimSpace(summary.ObservationID) == "" {
		return append([]turnObservation{}, observations...)
	}
	compactedObservationIDs := stringSet(append(append([]string{}, summary.CompactedObservationIDs...), summary.OmittedToolObservationIDs...))
	omittedToolIDs := stringSet(summary.OmittedToolObservationIDs)
	summarizedAssistantIDs := stringSet(summary.SummarizedAssistantObservationIDs)
	promptObservations := []turnObservation{summaryObservation(summary)}
	for _, observation := range observations {
		observationID := strings.TrimSpace(observation.ObservationID)
		if compactedObservationIDs[observationID] && !pinnedObservationIDs[observationID] {
			if omittedToolIDs[observationID] && !summarizedAssistantIDs[observationID] && observation.AssistantText != "" {
				promptObservations = append(promptObservations, assistantOnlyObservation(observation))
			}
			continue
		}
		if observation.Action == "context_summary" {
			continue
		}
		if summarizedAssistantIDs[observationID] && !pinnedObservationIDs[observationID] {
			observation.AssistantText = ""
			observation.ModelReasoning = ""
			observation.ModelReasoningField = ""
		}
		promptObservations = append(promptObservations, observation)
	}
	return promptObservations
}

func summaryObservation(summary TaskContextSummary) turnObservation {
	content := summaryPromptContent(summary)
	return turnObservation{
		ObservationID: strings.TrimSpace(summary.ObservationID),
		Action:        "context_summary",
		Output:        toolcontract.ToolOutput{Content: content},
		Summary:       "Compacted task context through " + strings.TrimSpace(summary.CompactedThroughObservationID) + ": " + content,
	}
}

func pinnedPromptObservationIDs(observations []turnObservation, events []agentcontract.TaskEvent) map[string]bool {
	pinnedObservationIDs := completionEvidenceObservationIDs(events)
	pinActiveFailureDebtObservations(pinnedObservationIDs, observations)
	for _, observation := range observations {
		if len(observation.Effects) > 0 || toolcontract.IsArtifactDeliveryTool(observation.Tool) {
			pinnedObservationIDs[observation.ObservationID] = true
		}
	}
	return pinnedObservationIDs
}

func pinActiveFailureDebtObservations(pinnedObservationIDs map[string]bool, observations []turnObservation) {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return
	}
	for index, observation := range observations {
		if strings.TrimSpace(observation.ObservationID) != strings.TrimSpace(failureDebt.LatestFailure.ObservationID) {
			continue
		}
		for _, activeObservation := range observations[index:] {
			pinnedObservationIDs[strings.TrimSpace(activeObservation.ObservationID)] = true
		}
		return
	}
}

func completionEvidenceObservationIDs(events []agentcontract.TaskEvent) map[string]bool {
	observationIDs := map[string]bool{}
	for _, event := range events {
		if strings.TrimSpace(event.Name) != agentcontract.TaskEventAgentAction {
			continue
		}
		var actionDocument turnActionDocument
		if json.Unmarshal([]byte(event.Body), &actionDocument) != nil {
			continue
		}
		actionDocument = normalizeParsedEvidence(actionDocument)
		for _, reference := range actionDocument.CompletionEvidence {
			if observationID := strings.TrimSpace(reference.ObservationID); observationID != "" {
				observationIDs[observationID] = true
			}
		}
		for _, reviewItem := range actionDocument.QualityReview {
			for _, reference := range reviewItem.Evidence {
				if observationID := strings.TrimSpace(reference.ObservationID); observationID != "" {
					observationIDs[observationID] = true
				}
			}
		}
	}
	return observationIDs
}

func (agentTurnRunner *AgentTurnRunner) generateTaskContextSummary(ctx context.Context, state agentTaskState, currentSummary TaskContextSummary) (TaskContextSummary, bool) {
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{{
			Role:    "system",
			Content: taskContextSummaryInstruction(),
		}, {
			Role:    "user",
			Content: taskContextSummaryInput(state, currentSummary),
		}},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "bluecollar_task_context_summary",
			Document:           taskContextSummarySchema(),
			IsStrictlyEnforced: true,
		},
	})
	if errorValue != nil {
		return TaskContextSummary{}, false
	}
	content, errorValue := decodeTaskContextSummaryContent(structuredResponse.Content)
	if errorValue != nil || !summaryEvidenceIsRecorded(content.Evidence, state.Observations) {
		return TaskContextSummary{}, false
	}
	return normalizeTaskContextSummary(TaskContextSummary{TaskContextSummaryContent: content}), true
}

func taskContextSummaryInstruction() string {
	return strings.Join([]string{
		"Summarize the non-tool model input and assistant output into the required task-state JSON object. Tool calls and tool results are excluded and will be judged separately.",
		"Preserve exact operational state needed for the next step.",
		"Preserve user constraints, later corrections, pending work and open questions. An assistant claim is not proof a tool succeeded.",
		"Do not turn system instructions, available capabilities or missing optional environment fields into pending user work. Include only user-established objectives and supported task state.",
		"never invent IDs/paths/URLs, copy them exactly from observations",
		"Cite provided observation IDs in evidence for facts drawn from assistant outputs. Use an empty evidence list for facts supported only by the input.",
		"Use empty arrays for fields with no supported facts.",
	}, "\n")
}

func taskContextSummaryInput(state agentTaskState, currentSummary TaskContextSummary) string {
	return marshalEventBody(map[string]any{
		"input":            nonToolSummaryInput(state),
		"currentSummary":   currentSummary.TaskContextSummaryContent,
		"assistantOutputs": assistantOutputsForSummary(state.Observations),
		"copyExactValues":  []string{"observationID", "URL", "path"},
	})
}

func normalizeTaskContextSummary(summary TaskContextSummary) TaskContextSummary {
	return TaskContextSummary{
		TaskContextSummaryContent:         normalizeSummaryContent(summary.TaskContextSummaryContent),
		ObservationID:                     strings.TrimSpace(summary.ObservationID),
		CompactedThroughObservationID:     strings.TrimSpace(summary.CompactedThroughObservationID),
		CompactedObservationIDs:           normalizeTaskContextSummaryList(summary.CompactedObservationIDs, 0),
		OmittedToolObservationIDs:         normalizeTaskContextSummaryList(summary.OmittedToolObservationIDs, 0),
		SummarizedAssistantObservationIDs: normalizeTaskContextSummaryList(summary.SummarizedAssistantObservationIDs, 0),
		CompactedConversationMessageKeys:  append([]string{}, summary.CompactedConversationMessageKeys...),
		AccountedTaskEventIDs:             summary.AccountedTaskEventIDs,
		RetainedObservations:              summary.RetainedObservations,
		CompactedObservationCount:         summary.CompactedObservationCount,
		CompactedToolCallCount:            summary.CompactedToolCallCount,
		SavedTranscript:                   strings.TrimSpace(summary.SavedTranscript),
	}
}

func normalizeTaskContextSummaryList(values []string, limit int) []string {
	normalizedValues := []string{}
	seenValues := map[string]bool{}
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		normalizedValues = append(normalizedValues, trimmedValue)
		if limit > 0 && len(normalizedValues) >= limit {
			break
		}
	}
	return normalizedValues
}

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
