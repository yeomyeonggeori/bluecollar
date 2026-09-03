package loop

import (
	"context"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const defaultCompactionTriggerTokens = 96000
const taskContextCompactionRecentObservationCount = 10
const taskContextCompactionMinimumNewObservations = 6
const taskContextCompactionMinimumNewCharacters = 20000
const taskContextSummaryMaxTokens = 1800

type TaskContextSummary struct {
	ObservationID                 string   `json:"observationID,omitempty"`
	CompactedThroughObservationID string   `json:"compactedThroughObservationID,omitempty"`
	CompactedObservationIDs       []string `json:"compactedObservationIDs,omitempty"`
	Goal                          string   `json:"goal,omitempty"`
	CompletedSteps                []string `json:"completedSteps,omitempty"`
	Artifacts                     []string `json:"artifacts,omitempty"`
	KeyDecisions                  []string `json:"keyDecisions,omitempty"`
	ExhaustedRecoveryRoutes       []string `json:"exhaustedRecoveryRoutes,omitempty"`
	ActiveFailureDebt             []string `json:"activeFailureDebt,omitempty"`
	NextPlan                      []string `json:"nextPlan,omitempty"`

	AccountedTaskEventIDs     []string          `json:"accountedTaskEventIDs,omitempty"`
	RetainedObservations      []turnObservation `json:"retainedObservations,omitempty"`
	CompactedObservationCount int               `json:"compactedObservationCount,omitempty"`
	CompactedToolCallCount    int               `json:"compactedToolCallCount,omitempty"`
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

func (agentTurnRunner *AgentTurnRunner) promptVisibleObservationsForAction(ctx context.Context, taskRunID string, state agentTaskState) []turnObservation {
	taskEvents := agentTurnRunner.taskRunService.ListTaskEvent(taskRunID)
	currentSummary := latestTaskContextSummary(state.ContextSummary, taskEvents)
	pinnedObservationIDs := pinnedPromptObservationIDs(state.Observations, taskEvents)
	promptObservations := promptVisibleObservations(state.Observations, currentSummary, pinnedObservationIDs)
	estimatedTokenCount := estimatePromptTokenCount(BuildAgentActionRequest(withPromptObservations(state, promptObservations)).Messages)
	if estimatedTokenCount <= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) {
		return promptObservations
	}
	promptObservations, estimatedTokenCount = agentTurnRunner.promptObservationsWithLongToolResultsPruned(taskRunID, state, promptObservations, pinnedObservationIDs, estimatedTokenCount)
	if estimatedTokenCount <= compactionTriggerTokenThreshold(state.Options.ContextWindowTokens) {
		return promptObservations
	}
	plan, shouldCompact := buildTaskContextCompactionPlan(state.Observations, currentSummary, pinnedObservationIDs)
	if !shouldCompact || compactionAlreadyFreedNothing(taskEvents, plan.CompactedThroughObservationID) {
		return promptObservations
	}
	summary, ok := agentTurnRunner.generateTaskContextSummary(ctx, state.Request, currentSummary, plan.CompactableObservations)
	if !ok {
		return promptObservations
	}
	summary.ObservationID = "context-summary-" + plan.CompactedThroughObservationID
	summary.CompactedThroughObservationID = plan.CompactedThroughObservationID
	summary.CompactedObservationIDs = append([]string{}, plan.CompactedObservationIDs...)
	replacedCharacters := observationsCharacterCount(plan.CompactableObservations)
	summaryCharacters := len(summaryObservation(summary).ContentText())
	if summaryCharacters >= replacedCharacters {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextCompactionFreedNothing, marshalEventBody(map[string]any{
			"compactedThroughObservationID": plan.CompactedThroughObservationID,
			"replacedCharacters":            replacedCharacters,
			"summaryCharacters":             summaryCharacters,
		}))
		return promptObservations
	}
	compactedObservations := promptVisibleObservations(state.Observations, summary, pinnedObservationIDs)
	summary = summaryAccountingForCompactedObservations(summary, currentSummary, compactedObservations, plan, taskEvents)
	summary = normalizeTaskContextSummary(summary)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextSummary, marshalEventBody(summary))
	return compactedObservations
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
	prunedTokenCount := estimatePromptTokenCount(BuildAgentActionRequest(withPromptObservations(state, prunedObservations)).Messages)
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
	summary.CompactedObservationCount = previousSummary.CompactedObservationCount + len(newlyCompactedObservations)
	summary.CompactedToolCallCount = previousSummary.CompactedToolCallCount + successfulToolCallCount(newlyCompactedObservations)
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
		if pinnedObservationIDs[strings.TrimSpace(observation.ObservationID)] {
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
	if strings.TrimSpace(summary.ObservationID) == "" || len(summary.CompactedObservationIDs) == 0 {
		return append([]turnObservation{}, observations...)
	}
	compactedObservationIDs := stringSet(summary.CompactedObservationIDs)
	promptObservations := []turnObservation{summaryObservation(summary)}
	for _, observation := range observations {
		observationID := strings.TrimSpace(observation.ObservationID)
		if compactedObservationIDs[observationID] && !pinnedObservationIDs[observationID] {
			continue
		}
		promptObservations = append(promptObservations, observation)
	}
	return promptObservations
}

func summaryObservation(summary TaskContextSummary) turnObservation {
	content := marshalEventBody(summaryAsTheModelReadsIt(summary))
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

func (agentTurnRunner *AgentTurnRunner) generateTaskContextSummary(ctx context.Context, request AgentTurnRequest, currentSummary TaskContextSummary, observations []turnObservation) (TaskContextSummary, bool) {
	maxTokens := taskContextSummaryMaxTokens
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: []model.Message{{
			Role:    "system",
			Content: taskContextSummaryInstruction(),
		}, {
			Role:    "user",
			Content: taskContextSummaryInput(request, currentSummary, observations),
		}},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "bluecollar_task_context_summary",
			Document:           taskContextSummarySchema(),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: model.GenerationOptions{MaxTokens: &maxTokens},
	})
	if errorValue != nil {
		return TaskContextSummary{}, false
	}
	var summary TaskContextSummary
	if json.Unmarshal([]byte(structuredResponse.Content), &summary) != nil {
		return TaskContextSummary{}, false
	}
	return normalizeTaskContextSummary(summary), true
}

func taskContextSummaryInstruction() string {
	return strings.Join([]string{
		"Summarize old task observations into a rolling TaskContextSummary JSON object.",
		"Preserve exact operational state needed for the next step.",
		"never invent IDs/paths/URLs, copy them exactly from observations",
		"Use empty arrays for fields with no supported facts.",
	}, "\n")
}

func taskContextSummaryInput(request AgentTurnRequest, currentSummary TaskContextSummary, observations []turnObservation) string {
	return marshalEventBody(map[string]any{
		"goal":            strings.TrimSpace(request.Prompt),
		"currentSummary":  normalizeTaskContextSummary(currentSummary),
		"observations":    observations,
		"responseFields":  []string{"goal", "completedSteps", "artifacts", "keyDecisions", "exhaustedRecoveryRoutes", "activeFailureDebt", "nextPlan"},
		"copyExactValues": []string{"observationID", "siteID", "URL", "path"},
	})
}

func taskContextSummarySchema() string {
	return `{"type":"object","properties":{"goal":{"type":"string"},"completedSteps":{"type":"array","items":{"type":"string"}},"artifacts":{"type":"array","items":{"type":"string"}},"keyDecisions":{"type":"array","items":{"type":"string"}},"exhaustedRecoveryRoutes":{"type":"array","items":{"type":"string"}},"activeFailureDebt":{"type":"array","items":{"type":"string"}},"nextPlan":{"type":"array","items":{"type":"string"}}},"required":["goal","completedSteps","artifacts","keyDecisions","exhaustedRecoveryRoutes","activeFailureDebt","nextPlan"],"additionalProperties":false}`
}

func summaryAsTheModelReadsIt(summary TaskContextSummary) TaskContextSummary {
	summary.AccountedTaskEventIDs = nil
	summary.RetainedObservations = nil
	return normalizeTaskContextSummary(summary)
}

func normalizeTaskContextSummary(summary TaskContextSummary) TaskContextSummary {
	return TaskContextSummary{
		ObservationID:                 strings.TrimSpace(summary.ObservationID),
		CompactedThroughObservationID: strings.TrimSpace(summary.CompactedThroughObservationID),
		CompactedObservationIDs:       normalizeTaskContextSummaryList(summary.CompactedObservationIDs, 64),
		AccountedTaskEventIDs:         summary.AccountedTaskEventIDs,
		RetainedObservations:          summary.RetainedObservations,
		CompactedObservationCount:     summary.CompactedObservationCount,
		CompactedToolCallCount:        summary.CompactedToolCallCount,
		Goal:                          truncateText(compactWhitespace(summary.Goal), 500),
		CompletedSteps:                normalizeTaskContextSummaryList(summary.CompletedSteps, 24),
		Artifacts:                     normalizeTaskContextSummaryList(summary.Artifacts, 24),
		KeyDecisions:                  normalizeTaskContextSummaryList(summary.KeyDecisions, 24),
		ExhaustedRecoveryRoutes:       normalizeTaskContextSummaryList(summary.ExhaustedRecoveryRoutes, 16),
		ActiveFailureDebt:             normalizeTaskContextSummaryList(summary.ActiveFailureDebt, 16),
		NextPlan:                      normalizeTaskContextSummaryList(summary.NextPlan, 16),
	}
}

func normalizeTaskContextSummaryList(values []string, limit int) []string {
	normalizedValues := []string{}
	seenValues := map[string]bool{}
	for _, value := range values {
		trimmedValue := truncateText(compactWhitespace(value), 500)
		if trimmedValue == "" || seenValues[trimmedValue] {
			continue
		}
		seenValues[trimmedValue] = true
		normalizedValues = append(normalizedValues, trimmedValue)
		if len(normalizedValues) >= limit {
			break
		}
	}
	return normalizedValues
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
