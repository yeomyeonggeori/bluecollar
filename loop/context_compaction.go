package loop

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

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
