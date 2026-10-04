package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const contextSelectionSchemaName = "bluecollar_context_selection"

func stateWithContextSummary(state agentTaskState, summary TaskContextSummary, pinnedIDs map[string]bool) agentTaskState {
	state.Observations = promptVisibleObservations(state.Observations, summary, pinnedIDs)
	compactedMessages := map[string]int{}
	for _, key := range summary.CompactedConversationMessageKeys {
		compactedMessages[key]++
	}
	messages := state.Request.VisibleContext.Messages
	state.Request.VisibleContext.Messages = nil
	for index, message := range messages {
		key := conversationMessageKey(message)
		if index < len(messages)-1 && compactedMessages[key] > 0 {
			compactedMessages[key]--
			continue
		}
		state.Request.VisibleContext.Messages = append(state.Request.VisibleContext.Messages, message)
	}
	return state
}

func conversationMessageKey(message agentcontract.VisibleContextMessage) string {
	digest := sha256.Sum256([]byte(marshalEventBody(message)))
	return hex.EncodeToString(digest[:])
}

func contextCompactionBoundary(state agentTaskState) string {
	digest := sha256.Sum256([]byte(marshalEventBody(map[string]any{"input": nonToolSummaryInput(state), "observations": state.Observations})))
	return hex.EncodeToString(digest[:])
}

func hasCompactableConversation(state agentTaskState) bool {
	return len(state.Request.VisibleContext.Messages) > 1 && len(marshalEventBody(state.Request.VisibleContext.Messages)) >= taskContextCompactionMinimumNewCharacters
}

func (runner *AgentTurnRunner) selectContextForAction(ctx context.Context, taskRunID string, original agentTaskState, visible agentTaskState, previous TaskContextSummary, summary TaskContextSummary, plan taskContextCompactionPlan, pinnedIDs map[string]bool, events []agentcontract.TaskEvent) agentTaskState {
	summary = summaryKeepingProjection(summary, previous, plan)
	candidates := toolSelectionCandidates(plan.CompactableObservations, summary, pinnedIDs)
	omitted := runner.irrelevantToolObservationIDs(ctx, taskRunID, visible, summary, candidates)
	omitted = omissionsKeepingDependencies(omitted, visible.Observations)
	summary.OmittedToolObservationIDs = appendMissingStrings(summary.OmittedToolObservationIDs, omitted)
	selected := stateWithContextSummary(original, summary, pinnedIDs)
	if runner.estimateActionPromptTokenCount(selected) > compactionTriggerTokenThreshold(original.Options.ContextWindowTokens) {
		summary = summaryReplacingConversation(summary, visible, plan, pinnedIDs)
		selected = stateWithContextSummary(original, summary, pinnedIDs)
	}
	return runner.commitSelectedContext(ctx, taskRunID, original, visible, selected, previous, summary, plan, pinnedIDs, events)
}

func summaryKeepingProjection(summary TaskContextSummary, previous TaskContextSummary, plan taskContextCompactionPlan) TaskContextSummary {
	summary.ObservationID = "context-summary-" + plan.AttemptKey
	summary.CompactedThroughObservationID = firstNonEmptyString(plan.CompactedThroughObservationID, previous.CompactedThroughObservationID)
	summary.CompactedObservationIDs = append([]string{}, previous.CompactedObservationIDs...)
	summary.OmittedToolObservationIDs = append([]string{}, previous.OmittedToolObservationIDs...)
	summary.SummarizedAssistantObservationIDs = append([]string{}, previous.SummarizedAssistantObservationIDs...)
	summary.CompactedConversationMessageKeys = append([]string{}, previous.CompactedConversationMessageKeys...)
	summary.SavedTranscript = previous.SavedTranscript
	return summary
}

func toolSelectionCandidates(observations []turnObservation, summary TaskContextSummary, pinnedIDs map[string]bool) []turnObservation {
	citedIDs := map[string]bool{}
	for _, evidence := range summary.Evidence {
		for _, observationID := range evidence.ObservationIDs {
			citedIDs[observationID] = true
		}
	}
	candidates := []turnObservation{}
	for _, observation := range observations {
		if observation.Tool != "" && !pinnedIDs[observation.ObservationID] && !citedIDs[observation.ObservationID] {
			candidates = append(candidates, observation)
		}
	}
	return candidates
}

func (runner *AgentTurnRunner) irrelevantToolObservationIDs(ctx context.Context, taskRunID string, state agentTaskState, summary TaskContextSummary, candidates []turnObservation) []string {
	if len(candidates) == 0 {
		return nil
	}
	if runner.decisionModel == nil {
		runner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextCompactionFreedNothing, marshalEventBody(map[string]string{"stage": "context_selection", "error": "decision model is not configured; tool records retained"}))
		return nil
	}
	questions := contextSelectionQuestions(candidates)
	decisionModel := observedDecisionModel{decisionModel: runner.decisionModel, observe: runner.llmCallObserverForTaskRun(taskRunID), schemaName: contextSelectionSchemaName}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: map[string]any{"input": nonToolSummaryInput(state), "taskState": summary.TaskContextSummaryContent, "toolRecords": candidates}, Questions: questions})
	if errorValue != nil {
		runner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextCompactionFreedNothing, marshalEventBody(map[string]string{"stage": "context_selection", "error": errorValue.Error()}))
		return nil
	}
	omitted := []string{}
	for _, observation := range candidates {
		answer, isAnswered := response.Answers[observation.ObservationID]
		if isAnswered && answer.Type == model.DecisionQuestionTypeChoice && answer.Choice == "false" {
			omitted = append(omitted, observation.ObservationID)
		}
	}
	return omitted
}

func contextSelectionQuestions(candidates []turnObservation) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index, observation := range candidates {
		questions[observation.ObservationID] = model.ChoiceQuestion{
			Instructions:       fmt.Sprintf("Is toolRecords[%d] needed to continue the current request correctly? Read its input and result together with the taskState, original input and other records. Keep evidence of completion, unresolved failures, user constraints, exact identifiers and dependencies needed by another retained record. An assistant claim or an omission from taskState is not evidence this record is irrelevant. When uncertain, answer true.", index),
			OptionDescriptions: map[string]string{"true": "needed or uncertain; retain the record", "false": "unrelated and safe to omit from model context"},
		}.Question()
	}
	return questions
}

func summaryReplacingConversation(summary TaskContextSummary, state agentTaskState, plan taskContextCompactionPlan, pinnedIDs map[string]bool) TaskContextSummary {
	for _, observation := range plan.CompactableObservations {
		if pinnedIDs[observation.ObservationID] {
			continue
		}
		if observation.Tool == "" {
			summary.CompactedObservationIDs = appendMissingStrings(summary.CompactedObservationIDs, []string{observation.ObservationID})
		} else if observation.AssistantText != "" || observation.ModelReasoning != "" {
			summary.SummarizedAssistantObservationIDs = appendMissingStrings(summary.SummarizedAssistantObservationIDs, []string{observation.ObservationID})
		}
	}
	for index, message := range state.Request.VisibleContext.Messages {
		if index < len(state.Request.VisibleContext.Messages)-1 {
			summary.CompactedConversationMessageKeys = append(summary.CompactedConversationMessageKeys, conversationMessageKey(message))
		}
	}
	return summary
}

func (runner *AgentTurnRunner) commitSelectedContext(ctx context.Context, taskRunID string, original agentTaskState, visible agentTaskState, selected agentTaskState, previous TaskContextSummary, summary TaskContextSummary, plan taskContextCompactionPlan, pinnedIDs map[string]bool, events []agentcontract.TaskEvent) agentTaskState {
	before := runner.estimateActionPromptTokenCount(visible)
	after := runner.estimateActionPromptTokenCount(selected)
	if after >= before {
		runner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextCompactionFreedNothing, marshalEventBody(map[string]any{"compactedThroughObservationID": plan.AttemptKey, "estimatedTokensBefore": before, "estimatedTokensAfter": after}))
		return visible
	}
	plan.CompactableObservations = newlyRemovedObservations(visible.Observations, summary)
	plan.CompactedObservationIDs = observationIDsOf(plan.CompactableObservations)
	plan.ConversationMessages = newlyCompactedConversationMessages(visible.Request.VisibleContext.Messages, summary)
	plan.PreviousTranscript = previous.SavedTranscript
	summary.SavedTranscript = firstNonEmptyString(runner.saveCompactedTranscript(ctx, taskRunID, original.Request.WorkspaceRootPath, plan), previous.SavedTranscript)
	selected = stateWithContextSummary(original, summary, pinnedIDs)
	if runner.estimateActionPromptTokenCount(selected) >= before {
		runner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextCompactionFreedNothing, marshalEventBody(map[string]any{"compactedThroughObservationID": plan.AttemptKey, "stage": "saved_transcript", "estimatedTokensBefore": before, "estimatedTokensAfter": runner.estimateActionPromptTokenCount(selected)}))
		return visible
	}
	summary = summaryAccountingForCompactedObservations(summary, previous, selected.Observations, plan, events)
	runner.appendEvent(taskRunID, agentcontract.TaskEventAgentContextSummary, marshalEventBody(normalizeTaskContextSummary(summary)))
	return selected
}

func newlyRemovedObservations(observations []turnObservation, summary TaskContextSummary) []turnObservation {
	removedIDs := stringSet(append(append([]string{}, summary.CompactedObservationIDs...), summary.OmittedToolObservationIDs...))
	summarizedIDs := stringSet(summary.SummarizedAssistantObservationIDs)
	removed := []turnObservation{}
	for _, observation := range observations {
		if removedIDs[observation.ObservationID] || (summarizedIDs[observation.ObservationID] && (observation.AssistantText != "" || observation.ModelReasoning != "")) {
			removed = append(removed, observation)
		}
	}
	return removed
}

func newlyCompactedConversationMessages(messages []agentcontract.VisibleContextMessage, summary TaskContextSummary) []agentcontract.VisibleContextMessage {
	compactedKeys := stringSet(summary.CompactedConversationMessageKeys)
	compacted := []agentcontract.VisibleContextMessage{}
	for _, message := range messages {
		if compactedKeys[conversationMessageKey(message)] {
			compacted = append(compacted, message)
		}
	}
	return compacted
}

func omissionsKeepingDependencies(omitted []string, observations []turnObservation) []string {
	omittedIDs := stringSet(omitted)
	for {
		removedCount := len(omittedIDs)
		for _, observation := range observations {
			if omittedIDs[observation.ObservationID] {
				continue
			}
			delete(omittedIDs, observation.RepeatsObservationID)
			for _, observationID := range observation.RelatedResultIDs {
				delete(omittedIDs, observationID)
			}
		}
		if len(omittedIDs) == removedCount {
			break
		}
	}
	keptOmissions := []string{}
	for _, observationID := range omitted {
		if omittedIDs[observationID] {
			keptOmissions = append(keptOmissions, observationID)
		}
	}
	return keptOmissions
}
