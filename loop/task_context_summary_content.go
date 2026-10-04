package loop

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type taskSummarySchemaDefinition struct {
	document string
	resolved *jsonschema.Resolved
}

var taskSummarySchema = sync.OnceValues(func() (taskSummarySchemaDefinition, error) {
	options := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[[]string](): {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
	}}
	evidenceSchema, errorValue := jsonschema.For[TaskContextSummaryEvidence](options)
	if errorValue != nil {
		return taskSummarySchemaDefinition{}, errorValue
	}
	options.TypeSchemas[reflect.TypeFor[[]TaskContextSummaryEvidence]()] = &jsonschema.Schema{Type: "array", Items: evidenceSchema}
	schema, errorValue := jsonschema.For[TaskContextSummaryContent](options)
	if errorValue != nil {
		return taskSummarySchemaDefinition{}, errorValue
	}
	resolved, errorValue := schema.Resolve(nil)
	if errorValue != nil {
		return taskSummarySchemaDefinition{}, errorValue
	}
	document, errorValue := json.Marshal(schema)
	return taskSummarySchemaDefinition{document: string(document), resolved: resolved}, errorValue
})

func taskContextSummarySchema() string {
	schema, _ := taskSummarySchema()
	return schema.document
}

func decodeTaskContextSummaryContent(document string) (TaskContextSummaryContent, error) {
	schema, errorValue := taskSummarySchema()
	if errorValue != nil {
		return TaskContextSummaryContent{}, errorValue
	}
	var value any
	if errorValue := json.Unmarshal([]byte(document), &value); errorValue != nil {
		return TaskContextSummaryContent{}, errorValue
	}
	if errorValue := schema.resolved.Validate(value); errorValue != nil {
		return TaskContextSummaryContent{}, errorValue
	}
	var content TaskContextSummaryContent
	errorValue = json.Unmarshal([]byte(document), &content)
	return content, errorValue
}

func normalizeSummaryContent(content TaskContextSummaryContent) TaskContextSummaryContent {
	content.Goal = strings.TrimSpace(content.Goal)
	content.Context = strings.TrimSpace(content.Context)
	for _, values := range []*[]string{&content.Constraints, &content.CompletedSteps, &content.PendingSteps, &content.Artifacts, &content.KeyDecisions, &content.ExhaustedRecoveryRoutes, &content.ActiveFailureDebt, &content.OpenQuestions, &content.NextPlan} {
		*values = normalizeTaskContextSummaryList(*values, 0)
	}
	content.Evidence = append([]TaskContextSummaryEvidence{}, content.Evidence...)
	return content
}

func summaryEvidenceIsRecorded(evidence []TaskContextSummaryEvidence, observations []turnObservation) bool {
	recordedIDs := stringSet(observationIDsOf(observations))
	for _, item := range evidence {
		for _, observationID := range item.ObservationIDs {
			if !recordedIDs[observationID] {
				return false
			}
		}
	}
	return true
}

type summaryAssistantOutput struct {
	ObservationID string `json:"observationID"`
	Text          string `json:"text"`
	Reasoning     string `json:"reasoning,omitempty"`
}

func assistantOutputsForSummary(observations []turnObservation) []summaryAssistantOutput {
	outputs := []summaryAssistantOutput{}
	for _, observation := range observations {
		if observation.AssistantText == "" && observation.ModelReasoning == "" {
			continue
		}
		outputs = append(outputs, summaryAssistantOutput{ObservationID: observation.ObservationID, Text: observation.AssistantText, Reasoning: observation.ModelReasoning})
	}
	return outputs
}

func nonToolSummaryInput(state agentTaskState) map[string]any {
	return map[string]any{
		"request":               state.Request.Prompt,
		"conversation":          state.Request.VisibleContext,
		"inputParts":            state.Request.InputParts,
		"activeGoal":            state.Request.ActiveGoal,
		"priorTask":             state.Request.PriorTask,
		"scheduledRun":          state.Request.ScheduledRun,
		"memoryFacts":           state.Request.MemoryFacts,
		"workspaceInstructions": state.Request.InstructionPrompt,
		"runtimeObservations":   nonToolObservations(state.Observations),
		"executionState":        state.ExecutionState,
		"outcomeContract":       state.Request.OutcomeContract,
	}
}

func nonToolObservations(observations []turnObservation) []turnObservation {
	kept := []turnObservation{}
	for _, observation := range observations {
		if observation.Tool == "" && observation.Action != "context_summary" {
			kept = append(kept, observation)
		}
	}
	return kept
}

func assistantOnlyObservation(observation turnObservation) turnObservation {
	return turnObservation{
		ObservationID:       observation.ObservationID,
		Action:              "assistant_message",
		AssistantText:       observation.AssistantText,
		ModelReasoning:      observation.ModelReasoning,
		ModelReasoningField: observation.ModelReasoningField,
		Output:              toolcontract.ToolOutput{Content: observation.AssistantText},
		Summary:             observation.AssistantText,
	}
}

func countRemovedObservations(removed []turnObservation, retained []turnObservation) int {
	retainedIDs := stringSet(observationIDsOf(retained))
	count := 0
	for _, observation := range removed {
		if !retainedIDs[observation.ObservationID] {
			count++
		}
	}
	return count
}

func countRemovedToolCalls(removed []turnObservation, retained []turnObservation) int {
	retainedToolIDs := map[string]bool{}
	for _, observation := range retained {
		if observation.Tool != "" {
			retainedToolIDs[observation.ObservationID] = true
		}
	}
	removedCalls := []turnObservation{}
	for _, observation := range removed {
		if !retainedToolIDs[observation.ObservationID] {
			removedCalls = append(removedCalls, observation)
		}
	}
	return successfulToolCallCount(removedCalls)
}

func summaryPromptContent(summary TaskContextSummary) string {
	if len(summary.CompactedObservationIDs)+len(summary.SummarizedAssistantObservationIDs)+len(summary.CompactedConversationMessageKeys) == 0 {
		return marshalEventBody(map[string]string{"savedTranscript": summary.SavedTranscript})
	}
	return marshalEventBody(map[string]any{"taskState": summary.TaskContextSummaryContent, "savedTranscript": summary.SavedTranscript})
}
