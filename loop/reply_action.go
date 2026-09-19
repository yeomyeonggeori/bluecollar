package loop

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) deliverReplyAttachments(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (turnActionDocument, []toolcontract.FileAttachment) {
	if len(actionDocument.Attachments) == 0 {
		return actionDocument, nil
	}
	observationID := nextObservationIDForObservations(state.Observations)
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, observationID, toolcontract.FileDeliverToolName, replyAttachmentToolInput(actionDocument.Attachments), request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	if observation.Failed() {
		return actionDocument, nil
	}
	actionDocument.CompletionEvidenceIDs = appendUniqueStrings(actionDocument.CompletionEvidenceIDs, observation.ObservationID)
	actionDocument.CompletionEvidence = evidenceReferencesFromIDs(actionDocument.CompletionEvidenceIDs)
	return actionDocument, observation.Attachments
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
	actionDocument, attachments := agentTurnRunner.deliverReplyAttachments(ctx, taskRunID, request, state, successfulToolCalls, actionDocument)
	if actionDocument.ExpectsAnswer {
		return agentTurnRunner.askForReplyAnswer(ctx, taskRunID, stepID, request, state, successfulToolCalls, actionDocument)
	}
	observation := agentTurnRunner.sendReply(ctx, taskRunID, request, state, actionDocument, attachments)
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "reply", observation.ContentText())
	return AgentTurnResult{}, false
}

func (agentTurnRunner *AgentTurnRunner) askForReplyAnswer(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (AgentTurnResult, bool) {
	observationID := nextObservationIDForObservations(state.Observations)
	question := toolcontract.MarshalToolInput(map[string]any{"question": strings.TrimSpace(actionDocument.Message)})
	observation := agentTurnRunner.invokeTool(ctx, request.ToolSet, taskRunID, observationID, toolcontract.AskInputToolName, question, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, "")
	pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments)
	if !isPaused {
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "reply", observation.ContentText())
		return AgentTurnResult{}, false
	}
	agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "reply", observation.ContentText())
	return pausedResult, true
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
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentReplySent, marshalEventBody(replyReceipt{Status: "delivered", Message: message, AttachmentCount: len(attachments)}))
	return observation
}

type replyReceipt struct {
	Status          string `json:"status"`
	Message         string `json:"message,omitempty"`
	Reason          string `json:"reason,omitempty"`
	AttachmentCount int    `json:"attachmentCount"`
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
