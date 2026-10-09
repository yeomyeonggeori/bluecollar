package loop

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) deliverFinishAttachments(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (turnActionDocument, bool) {
	deliveredDocument, _, isDelivered := agentTurnRunner.deliverReplyAttachments(ctx, taskRunID, request, state, successfulToolCalls, actionDocument)
	if !isDelivered {
		return deliveredDocument, false
	}
	return agentTurnRunner.redeliverFilesChangeableSinceDelivery(ctx, taskRunID, request, state, successfulToolCalls, deliveredDocument)
}

func (agentTurnRunner *AgentTurnRunner) redeliverFilesChangeableSinceDelivery(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation, actionDocument turnActionDocument) (turnActionDocument, bool) {
	changeable := stagedFilesChangeableSinceDelivery(state.Request.ToolSet, state.Observations, state.DeliveredAttachmentPaths)
	if len(changeable) == 0 {
		return actionDocument, true
	}
	redelivery := actionDocument
	redelivery.Attachments = replyAttachmentsOf(changeable)
	redeliveredDocument, _, isRedelivered := agentTurnRunner.deliverReplyAttachments(ctx, taskRunID, request, state, successfulToolCalls, redelivery)
	redeliveredDocument.Attachments = actionDocument.Attachments
	return redeliveredDocument, isRedelivered
}

func stagedFilesChangeableSinceDelivery(toolSet *toolcontract.ToolSet, observations []turnObservation, deliveredPaths []string) []toolcontract.FileAttachment {
	changeable := []toolcontract.FileAttachment{}
	for _, attachment := range attachmentsNotYetDelivered(deliveredAttachments(observations), deliveredPaths) {
		deliveryIndex := latestDeliveryIndex(observations, attachment.DevicePath)
		if anyChangeableCallAfter(toolSet, observations, deliveryIndex) {
			changeable = append(changeable, attachment)
		}
	}
	return changeable
}

func latestDeliveryIndex(observations []turnObservation, devicePath string) int {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if toolexposure.IsArtifactDeliveryTool(observation.Tool) && !observation.Failed() && hasAttachmentDevicePath(observation.Attachments, devicePath) {
			return index
		}
	}
	return len(observations)
}

func anyChangeableCallAfter(toolSet *toolcontract.ToolSet, observations []turnObservation, index int) bool {
	for _, observation := range observations[min(index+1, len(observations)):] {
		if callCouldChangeFiles(toolSet, observation) {
			return true
		}
	}
	return false
}

func callCouldChangeFiles(toolSet *toolcontract.ToolSet, observation turnObservation) bool {
	if observation.ToolIsReadOnly || toolexposure.IsArtifactDeliveryTool(observation.Tool) || callWasRefusedBeforeRunning(observation) {
		return false
	}
	definition, isFound := toolSet.ToolDefinition(observation.Tool)
	return isFound && ToolDefinitionRequiresSideEffectEvidence(definition)
}

func callWasRefusedBeforeRunning(observation turnObservation) bool {
	return observation.Failure != nil && observation.Failure.Kind == toolcontract.FailurePolicyBlocked
}

func replyAttachmentsOf(attachments []toolcontract.FileAttachment) []replyAttachment {
	files := make([]replyAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		files = append(files, replyAttachment{Path: strings.TrimSpace(attachment.DevicePath), Filename: strings.TrimSpace(attachment.Filename)})
	}
	return files
}

func latestDeliveredVersions(observations []turnObservation, attachments []toolcontract.FileAttachment) []toolcontract.FileAttachment {
	latest := make([]toolcontract.FileAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		latest = append(latest, latestDeliveredVersion(observations, attachment))
	}
	return latest
}

func latestDeliveredVersion(observations []turnObservation, attachment toolcontract.FileAttachment) toolcontract.FileAttachment {
	delivery, isFound := latestDeliveryOf(observations, attachment.DevicePath)
	if !isFound {
		return attachment
	}
	for _, delivered := range delivery.Attachments {
		if strings.TrimSpace(delivered.DevicePath) == strings.TrimSpace(attachment.DevicePath) {
			return delivered
		}
	}
	return attachment
}
