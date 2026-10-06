package loop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func (agentTurnRunner *AgentTurnRunner) applyPendingSteeringEvents(taskRunID string, observations []turnObservation, appliedEventIDs map[string]bool) []turnObservation {
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != agentcontract.TaskEventAgentSteerReceived || appliedEventIDs[taskEvent.TaskEventID] {
			continue
		}
		var document struct {
			MessageID   string `json:"messageID"`
			Instruction string `json:"instruction"`
			Reason      string `json:"reason"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) != nil {
			continue
		}
		instruction := strings.TrimSpace(document.Instruction)
		if instruction == "" {
			continue
		}
		observation := newContentObservation(nextObservationIDForObservations(observations), "steer", "", "This is the latest user correction for the current task; update the plan before continuing.\n"+marshalEventBody(map[string]string{
			"instruction": instruction,
			"reason":      strings.TrimSpace(document.Reason),
			"messageID":   strings.TrimSpace(document.MessageID),
		}))
		observation.Summary = "User steering instruction: " + instruction
		observations = append(observations, observation)
		appliedEventIDs[taskEvent.TaskEventID] = true
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventTaskSteerApplied, marshalEventBody(map[string]string{
			"sourceEventID": taskEvent.TaskEventID,
			"observationID": observation.ObservationID,
			"messageID":     strings.TrimSpace(document.MessageID),
		}))
	}
	return observations
}

func appliedSteerEventIDsFromTaskEvents(taskEvents []agentcontract.TaskEvent) map[string]bool {
	eventIDs := map[string]bool{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.TaskEventTaskSteerApplied {
			continue
		}
		var document struct {
			SourceEventID string `json:"sourceEventID"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) == nil && strings.TrimSpace(document.SourceEventID) != "" {
			eventIDs[strings.TrimSpace(document.SourceEventID)] = true
		}
	}
	return eventIDs
}

func (agentTurnRunner *AgentTurnRunner) taskRunForRequest(request AgentTurnRequest) agentcontract.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return agentTurnRunner.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentTurnRunner *AgentTurnRunner) appendConversationBudgetEvent(taskRunID string, contextWindowTokens int) {
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentConversationBudget, marshalEventBody(map[string]any{
		"contextWindowTokens":        contextWindowTokens,
		"windowWasDeclared":          contextWindowTokens > 0,
		"compactionTriggerTokens":    compactionTriggerTokenThreshold(contextWindowTokens),
		"observationShareCharacters": compactionTriggerTokenThreshold(contextWindowTokens) * charactersPerToken / maxProgressObservations,
	}))
}

func (agentTurnRunner *AgentTurnRunner) appendTaskSourceEvent(taskRunID string, sourceReference string) {
	trimmedSourceReference := strings.TrimSpace(sourceReference)
	if trimmedSourceReference == "" {
		return
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentTaskSource, marshalEventBody(map[string]string{
		"sourceReference": trimmedSourceReference,
	}))
}

func (agentTurnRunner *AgentTurnRunner) saveStep(taskRunID string, taskStepID string, status agentcontract.TaskStatus, instruction string, output string) {
	agentTurnRunner.taskStepService.AddTaskStep(taskstate.TaskStep{
		TaskStepID:               taskStepID,
		TaskRunID:                taskRunID,
		AssignedAgentProfileName: "assistant",
		Instruction:              instruction,
		Status:                   status,
		Output:                   output,
	})
}

func (agentTurnRunner *AgentTurnRunner) appendEvent(taskRunID string, name string, body string) {
	agentTurnRunner.taskRunService.AppendTaskEvent(taskRunID, name, body)
}

func (agentTurnRunner *AgentTurnRunner) appendValidityReview(taskRunID string, phase string, validityState ValidityState) {
	if len(validityState.CheckedArtifacts) == 0 {
		return
	}
	body := map[string]any{
		"phase":            phase,
		"passed":           validityState.Passed,
		"checkedArtifacts": validityState.CheckedArtifacts,
		"invalidArtifacts": validityState.InvalidArtifacts,
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentValidityReview, marshalEventBody(body))
}

func (agentTurnRunner *AgentTurnRunner) appendQualityReview(taskRunID string, criteria []qualityCriterion, review []qualityReviewItem, observations []turnObservation) {
	if len(criteria) == 0 {
		return
	}
	qualityState := buildQualityState(criteria, review, observations)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentQualityReview, marshalEventBody(qualityState))
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(document)
}
