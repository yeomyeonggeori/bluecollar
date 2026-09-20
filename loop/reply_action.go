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
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: replyStatusNotDelivered, Reason: "attachment_failed", Detail: observation.FailureSummary(), AttachmentCount: len(actionDocument.Attachments)}))
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
	if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, state.Attachments); isCancelled {
		return cancelledResult, true
	}
	actionDocument, attachments, isDelivered := agentTurnRunner.deliverReplyAttachments(ctx, taskRunID, request, state, successfulToolCalls, actionDocument)
	if !isDelivered {
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "reply", lastObservationText(state.Observations))
		return AgentTurnResult{}, false
	}
	if actionDocument.ExpectsAnswer {
		return agentTurnRunner.askForReplyAnswer(ctx, taskRunID, stepID, request, state, successfulToolCalls, actionDocument)
	}
	receipt := agentTurnRunner.sendReply(ctx, taskRunID, request, state, actionDocument, attachments)
	agentTurnRunner.saveStep(taskRunID, stepID, replyStepStatus(receipt), "reply", marshalEventBody(receipt))
	return AgentTurnResult{}, false
}

func replyStepStatus(receipt replyReceipt) agentcontract.TaskStatus {
	if receipt.Status == replyStatusDelivered {
		return agentcontract.TaskStatusCompleted
	}
	return agentcontract.TaskStatusFailed
}

func (agentTurnRunner *AgentTurnRunner) askForReplyAnswer(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (AgentTurnResult, bool) {
	if !turnCanAskTheRequester(ctx, request) {
		return agentTurnRunner.refuseReplyQuestion(taskRunID, stepID, state, actionDocument)
	}
	observationID := nextObservationIDForObservations(state.Observations)
	observation := agentTurnRunner.invokeTool(ctx, state.Request.ToolSet.AllowingInternalTool(toolcontract.AskInputToolName), taskRunID, observationID, toolcontract.AskInputToolName, replyQuestionToolInput(actionDocument), request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments)
	if !isPaused {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(replyReceipt{Status: replyStatusNotDelivered, Reason: "ask_not_pending", Detail: askFailureReason(observation), Message: strings.TrimSpace(actionDocument.Message)}))
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "reply", observation.ContentText())
		return AgentTurnResult{}, false
	}
	agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "reply", observation.ContentText())
	return pausedResult, true
}

func turnCanAskTheRequester(ctx context.Context, request AgentTurnRequest) bool {
	return !toolcontract.IsDelegatedTurn(ctx) && requestCanAskTheUser(request)
}

func (agentTurnRunner *AgentTurnRunner) refuseReplyQuestion(taskRunID string, stepID string, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool) {
	observation := newContentObservation(nextObservationIDForObservations(state.Observations), "policy", "", "This run has nobody to answer a question: expectsAnswer is not available here. Decide with what you already have, or say what you would have asked in a final reply.")
	state.Observations = append(state.Observations, observation)
	receipt := replyReceipt{Status: replyStatusNotDelivered, Reason: "no_requester_to_answer", Detail: observation.ContentText(), Message: strings.TrimSpace(actionDocument.Message)}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplyFailed, marshalEventBody(receipt))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "reply", marshalEventBody(receipt))
	return AgentTurnResult{}, false
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

func (agentTurnRunner *AgentTurnRunner) sendReply(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, attachments []toolcontract.FileAttachment) replyReceipt {
	message := strings.TrimSpace(actionDocument.Message)
	if message == "" && len(attachments) == 0 {
		return agentTurnRunner.recordReplyReceipt(taskRunID, state, replyReceipt{Status: replyStatusNotDelivered, Reason: "empty_reply", Detail: "the reply carried neither a message nor an attachment"})
	}
	if request.CheckpointSender == nil {
		return agentTurnRunner.recordReplyReceipt(taskRunID, state, replyReceipt{Status: replyStatusNotDelivered, Reason: "missing_sender", Detail: "this run has no reply channel, so the words stayed here", Message: message, AttachmentCount: len(attachments)})
	}
	if !checkpointMessageAllowed(message, state.Observations) {
		return agentTurnRunner.recordReplyReceipt(taskRunID, state, replyReceipt{Status: replyStatusNotDelivered, Reason: "rate_limited_or_duplicate", Detail: "this task has already sent its progress updates, or this one repeats one of them", Message: message, AttachmentCount: len(attachments)})
	}
	message = agentTurnRunner.prepareFinishMessageForPlatform(ctx, request, message)
	if errorValue := request.CheckpointSender(ctx, AgentCheckpoint{TaskRunID: taskRunID, Message: message, Attachments: attachments}); errorValue != nil {
		return agentTurnRunner.recordReplyReceipt(taskRunID, state, replyReceipt{Status: replyStatusNotDelivered, Reason: "sender_failed", Detail: errorValue.Error(), Message: message, AttachmentCount: len(attachments)})
	}
	deliveredPaths := attachmentDevicePaths(attachments)
	state.LastModelMessage = ""
	state.DeliveredAttachmentPaths = appendUniqueStrings(state.DeliveredAttachmentPaths, deliveredPaths...)
	return agentTurnRunner.recordReplyReceipt(taskRunID, state, replyReceipt{Status: replyStatusDelivered, Message: message, AttachmentCount: len(attachments), AttachmentPaths: deliveredPaths})
}

func (agentTurnRunner *AgentTurnRunner) recordReplyReceipt(taskRunID string, state *agentTaskState, receipt replyReceipt) replyReceipt {
	receipt.ObservationID = nextObservationIDForObservations(state.Observations)
	state.Observations = append(state.Observations, replyReceiptObservation(receipt))
	agentTurnRunner.appendEvent(taskRunID, replyReceiptEventName(receipt), marshalEventBody(receipt))
	return receipt
}

func replyReceiptEventName(receipt replyReceipt) string {
	if receipt.Status == replyStatusDelivered {
		return agentcontract.TaskEventAgentReplySent
	}
	return agentcontract.TaskEventAgentReplyFailed
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

const (
	replyStatusDelivered    = "delivered"
	replyStatusNotDelivered = "not_delivered"
)

type replyReceipt struct {
	ObservationID   string   `json:"observationID,omitempty"`
	Status          string   `json:"status"`
	Message         string   `json:"message,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Detail          string   `json:"detail,omitempty"`
	AttachmentCount int      `json:"attachmentCount"`
	AttachmentPaths []string `json:"attachmentPaths,omitempty"`
}

func replyReceiptObservation(receipt replyReceipt) turnObservation {
	observation := newContentObservation(receipt.ObservationID, "reply", "", marshalEventBody(receipt))
	observation.Summary = replyReceiptSummary(receipt)
	return observation
}

func replyReceiptObservationFromTaskEvent(event agentcontract.TaskEvent) (turnObservation, bool) {
	if event.Name != agentcontract.TaskEventAgentReplySent && event.Name != agentcontract.TaskEventAgentReplyFailed {
		return turnObservation{}, false
	}
	var receipt replyReceipt
	if json.Unmarshal([]byte(event.Body), &receipt) != nil || strings.TrimSpace(receipt.ObservationID) == "" {
		return turnObservation{}, false
	}
	return replyReceiptObservation(receipt), true
}

func replyReceiptSummary(receipt replyReceipt) string {
	summary := "Reply " + receipt.Status
	if receipt.AttachmentCount > 0 {
		summary += " with " + strconv.Itoa(receipt.AttachmentCount) + " attachment(s)"
	}
	if detail := firstNonEmptyString(receipt.Detail, receipt.Reason); detail != "" {
		summary += ": " + detail
	}
	return summary + "."
}

func deliveredReplyMessage(observation turnObservation) (string, bool) {
	if observation.Action != "reply" {
		return "", false
	}
	var receipt replyReceipt
	if json.Unmarshal(observation.StructuredOutput(), &receipt) != nil || receipt.Status != replyStatusDelivered {
		return "", false
	}
	return receipt.Message, true
}
