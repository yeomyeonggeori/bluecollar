package loop

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
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
		if len(observation.Effects) > 0 || toolexposure.IsArtifactDeliveryTool(observation.Tool) {
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
