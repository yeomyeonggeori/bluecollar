package loop

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) deliverReplyAttachments(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (turnActionDocument, []toolcontract.FileAttachment, bool) {
	if len(actionDocument.Attachments) == 0 {
		return actionDocument, nil, true
	}
	observationID := nextObservationIDForObservations(state.Observations)
	observation := agentTurnRunner.invokeTool(ctx, state.Request.ToolSet.AllowingInternalTool(toolcontract.FileDeliverToolName), taskRunID, observationID, toolcontract.FileDeliverToolName, replyAttachmentToolInput(actionDocument.Attachments), request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	if observation.Failed() {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: "not_delivered", Reason: observation.FailureSummary(), AttachmentCount: len(actionDocument.Attachments)}))
		return actionDocument, nil, false
	}
	actionDocument.CompletionEvidenceIDs = appendUniqueStrings(actionDocument.CompletionEvidenceIDs, observation.ObservationID)
	actionDocument.CompletionEvidence = evidenceReferencesFromIDs(actionDocument.CompletionEvidenceIDs)
	return actionDocument, observation.Attachments, true
}

func replyAttachmentToolInput(attachments []replyAttachment) json.RawMessage {
	files := make([]map[string]string, 0, len(attachments))
	for _, attachment := range attachments {
		file := map[string]string{"path": strings.TrimSpace(attachment.Path)}
		if filename := strings.TrimSpace(attachment.Filename); filename != "" {
			file["filename"] = filename
		}
		files = append(files, file)
	}
	return toolcontract.MarshalToolInput(map[string]any{"files": files})
}

func (agentTurnRunner *AgentTurnRunner) handleReplyAction(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (AgentTurnResult, bool) {
	actionDocument, attachments, isDelivered := agentTurnRunner.deliverReplyAttachments(ctx, taskRunID, request, state, successfulToolCalls, actionDocument)
	if !isDelivered {
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "reply", lastObservationText(state.Observations))
		return AgentTurnResult{}, false
	}
	if actionDocument.ExpectsAnswer {
		return agentTurnRunner.askForReplyAnswer(ctx, taskRunID, stepID, request, state, successfulToolCalls, actionDocument)
	}
	observation := agentTurnRunner.sendReply(ctx, taskRunID, request, state, actionDocument, attachments)
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "reply", observation.ContentText())
	return AgentTurnResult{}, false
}

func (agentTurnRunner *AgentTurnRunner) askForReplyAnswer(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (AgentTurnResult, bool) {
	observationID := nextObservationIDForObservations(state.Observations)
	observation := agentTurnRunner.invokeTool(ctx, state.Request.ToolSet.AllowingInternalTool(toolcontract.AskInputToolName), taskRunID, observationID, toolcontract.AskInputToolName, replyQuestionToolInput(actionDocument), request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments)
	if !isPaused {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: "not_delivered", Reason: askFailureReason(observation), Message: strings.TrimSpace(actionDocument.Message)}))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "reply", observation.ContentText())
		return AgentTurnResult{}, false
	}
	agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "reply", observation.ContentText())
	return pausedResult, true
}

func askFailureReason(observation turnObservation) string {
	if observation.Failed() {
		return observation.FailureSummary()
	}
	return "the question did not leave the task waiting for an answer"
}

func replyQuestionToolInput(actionDocument turnActionDocument) json.RawMessage {
	question := map[string]any{"question": strings.TrimSpace(actionDocument.Message)}
	if choices := trimmedNonEmptyStrings(actionDocument.Choices); len(choices) > 0 {
		question["choices"] = choices
	}
	return toolcontract.MarshalToolInput(question)
}

func trimmedNonEmptyStrings(values []string) []string {
	trimmed := []string{}
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			trimmed = appendUniqueStrings(trimmed, trimmedValue)
		}
	}
	return trimmed
}

func lastObservationText(observations []turnObservation) string {
	if len(observations) == 0 {
		return ""
	}
	return observations[len(observations)-1].ContentText()
}

func (agentTurnRunner *AgentTurnRunner) sendReply(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, attachments []toolcontract.FileAttachment) turnObservation {
	message := strings.TrimSpace(actionDocument.Message)
	observationID := nextObservationIDForObservations(state.Observations)
	if message == "" && len(attachments) == 0 {
		observation := replyReceiptObservation(observationID, "not_delivered", "reply carried neither a message nor an attachment", 0)
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: "not_delivered", Reason: "empty_reply"}))
		return observation
	}
	if request.CheckpointSender == nil {
		observation := replyReceiptObservation(observationID, "not_delivered", "this run has no reply channel, so the words stayed here", len(attachments))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: "not_delivered", Reason: "missing_sender", AttachmentCount: len(attachments)}))
		return observation
	}
	errorValue := request.CheckpointSender(ctx, AgentCheckpoint{TaskRunID: taskRunID, Message: message, Attachments: attachments})
	if errorValue != nil {
		observation := replyReceiptObservation(observationID, "not_delivered", errorValue.Error(), len(attachments))
		state.Observations = append(state.Observations, observation)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: "not_delivered", Reason: errorValue.Error(), AttachmentCount: len(attachments)}))
		return observation
	}
	observation := replyReceiptObservation(observationID, "delivered", "", len(attachments))
	state.Observations = append(state.Observations, observation)
	state.LastModelMessage = ""
	deliveredPaths := attachmentDevicePaths(attachments)
	state.DeliveredAttachmentPaths = appendUniqueStrings(state.DeliveredAttachmentPaths, deliveredPaths...)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplySent, marshalEventBody(replyReceipt{Status: "delivered", Message: message, AttachmentCount: len(attachments), AttachmentPaths: deliveredPaths}))
	return observation
}

func attachmentDevicePaths(attachments []toolcontract.FileAttachment) []string {
	devicePaths := []string{}
	for _, attachment := range attachments {
		if devicePath := strings.TrimSpace(attachment.DevicePath); devicePath != "" {
			devicePaths = appendUniqueStrings(devicePaths, devicePath)
		}
	}
	return devicePaths
}

func attachmentsNotYetDelivered(attachments []toolcontract.FileAttachment, deliveredPaths []string) []toolcontract.FileAttachment {
	if len(deliveredPaths) == 0 {
		return attachments
	}
	remaining := []toolcontract.FileAttachment{}
	for _, attachment := range attachments {
		if stringSliceContains(deliveredPaths, strings.TrimSpace(attachment.DevicePath)) {
			continue
		}
		remaining = append(remaining, attachment)
	}
	return remaining
}

func deliveredAttachmentPathsFromTaskEvents(events []agentcontract.TaskEvent) []string {
	devicePaths := []string{}
	for _, event := range events {
		if event.Name != agentcontract.TaskEventAgentReplySent {
			continue
		}
		var receipt replyReceipt
		if json.Unmarshal([]byte(event.Body), &receipt) != nil {
			continue
		}
		devicePaths = appendUniqueStrings(devicePaths, receipt.AttachmentPaths...)
	}
	return devicePaths
}

type replyReceipt struct {
	Status          string   `json:"status"`
	Message         string   `json:"message,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	AttachmentCount int      `json:"attachmentCount"`
	AttachmentPaths []string `json:"attachmentPaths,omitempty"`
}

func replyReceiptObservation(observationID string, status string, reason string, attachmentCount int) turnObservation {
	receipt := replyReceipt{Status: status, Reason: reason, AttachmentCount: attachmentCount}
	observation := newContentObservation(observationID, "reply", "", marshalEventBody(receipt))
	observation.Summary = replyReceiptSummary(receipt)
	return observation
}

func replyReceiptSummary(receipt replyReceipt) string {
	summary := "Reply " + receipt.Status
	if receipt.AttachmentCount > 0 {
		summary += " with " + strconv.Itoa(receipt.AttachmentCount) + " attachment(s)"
	}
	if receipt.Reason != "" {
		summary += ": " + receipt.Reason
	}
	return summary + "."
}
