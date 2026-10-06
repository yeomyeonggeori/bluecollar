package loop

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (agentTurnRunner *AgentTurnRunner) sendCheckpointMessage(ctx context.Context, taskRunID string, request AgentTurnRequest, actionDocument turnActionDocument, observations []turnObservation) []turnObservation {
	message := strings.TrimSpace(actionDocument.Message)
	if message == "" || agentTurnRunner == nil {
		return observations
	}
	if taskLevelWantsSingleFinalReply(request.TaskLevel) {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSkipped, marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"reason":   "task_level_xlow",
		}))
		return observations
	}
	if !checkpointMessageAllowed(message, observations) {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSkipped, marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"reason":   "rate_limited_or_duplicate",
		}))
		return observations
	}
	observation := newContentObservation(nextObservationIDForObservations(observations), "checkpoint", "", marshalEventBody(map[string]any{
		"message":  message,
		"toolName": actionDocument.ToolName,
	}))
	observation.Summary = message
	if request.CheckpointSender != nil {
		errorValue := request.CheckpointSender(ctx, AgentCheckpoint{
			TaskRunID: taskRunID,
			Message:   message,
			ToolName:  strings.TrimSpace(actionDocument.ToolName),
		})
		if errorValue != nil {
			observation.Output.Content = marshalEventBody(map[string]any{
				"message":  message,
				"toolName": actionDocument.ToolName,
				"status":   "failed",
				"error":    errorValue.Error(),
			})
			agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointFailed, marshalEventBody(map[string]any{
				"toolName": actionDocument.ToolName,
				"error":    errorValue.Error(),
			}))
			return append(observations, observation)
		}
		observation.Output.Content = marshalEventBody(map[string]any{
			"message":  message,
			"toolName": actionDocument.ToolName,
			"status":   "sent",
		})
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSent, marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"message":  message,
		}))
		return append(observations, observation)
	}
	observation.Output.Content = marshalEventBody(map[string]any{
		"message":  message,
		"toolName": actionDocument.ToolName,
		"status":   "skipped",
		"reason":   "missing_sender",
	})
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSkipped, marshalEventBody(map[string]any{
		"toolName": actionDocument.ToolName,
		"reason":   "missing_sender",
	}))
	return append(observations, observation)
}

func checkpointMessageAllowed(message string, observations []turnObservation) bool {
	normalizedMessage := normalizeCheckpointMessage(message)
	count := 0
	for _, observation := range observations {
		sentMessage, wasSent := midTaskMessageSent(observation)
		if !wasSent {
			continue
		}
		count++
		if normalizeCheckpointMessage(sentMessage) == normalizedMessage {
			return false
		}
	}
	return count < 3
}

func midTaskMessageSent(observation turnObservation) (string, bool) {
	if observation.Action == "checkpoint" {
		return checkpointObservationMessage(observation), true
	}
	return deliveredReplyMessage(observation)
}

func normalizeCheckpointMessage(message string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(message))), " ")
}

func checkpointObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(observation.StructuredOutput(), &document) == nil {
		return document.Message
	}
	return observation.Summary
}

func isWaitingForUser(status agentcontract.TaskStatus) bool {
	return status == agentcontract.TaskStatusWaitingApproval || status == agentcontract.TaskStatusWaitingUserInput
}

func toolObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(observation.StructuredOutput(), &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.Message)
}

func finishActionMessage(actionDocument turnActionDocument) string {
	return strings.TrimSpace(actionDocument.Message)
}

func approvalObservationUserFacingMessage(observation turnObservation) string {
	var document struct {
		UserFacingMessage string `json:"userFacingMessage"`
		Message           string `json:"message"`
		Question          string `json:"question"`
	}
	if json.Unmarshal(observation.StructuredOutput(), &document) != nil {
		return ""
	}
	return firstNonEmptyString(document.UserFacingMessage, document.Message, document.Question)
}
