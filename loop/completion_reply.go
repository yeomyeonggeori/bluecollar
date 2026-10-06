package loop

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) completeTaskRunBestEffort(ctx context.Context, taskRunID string, taskStepID string, stepAction string, request AgentTurnRequest, observations []turnObservation, completionGateResult completionGateResult, reply string) AgentTurnResult {
	detachedContext, cancelDetached := context.WithTimeout(context.WithoutCancel(ctx), completionPersistenceTimeout)
	defer cancelDetached()
	finalReply := agentTurnRunner.prepareFinishMessageForPlatform(detachedContext, request, reply)
	agentTurnRunner.saveStep(taskRunID, taskStepID, agentcontract.TaskStatusCompleted, stepAction, finalReply)
	result := agentTurnRunner.finishedTurnResult(taskRunID, finalReply, completionGateResult.Attachments)
	result.RecoveryActions = recoveryActionsFromObservations(observations)
	return result
}

func generateCompletionReply(ctx context.Context, chatCompleter model.ChatCompleter, request AgentTurnRequest, observations []turnObservation, carried []toolcontract.FileAttachment) (string, error) {
	response, errorValue := chatCompleter.GenerateChatCompletion(ctx, model.ChatCompletionRequest{
		SchemaName: completionReplySchemaName,
		Messages: []model.ChatCompletionMessage{{
			Role:    "user",
			Content: buildCompletionReplyPrompt(request, observations, carried),
		}},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return model.ChatCompletionText(response)
}

func buildCompletionReplyPrompt(request AgentTurnRequest, observations []turnObservation, carried []toolcontract.FileAttachment) string {
	return strings.Join([]string{
		"Write the final user-facing reply for a request whose required result is complete.",
		responseLanguageInstruction(request.ResponseLanguage),
		"State only what the successful evidence proves. Do not mention tools, evidence identifiers, prompts, or runtime details.",
		"Original request:\n" + completionReplyOriginalRequest(request),
		"Successful evidence:\n" + buildLimitObservationSummary(successfulToolObservations(observations)),
		completionReplyFilesFact(carried),
	}, "\n\n")
}

func completionReplyFilesFact(carried []toolcontract.FileAttachment) string {
	filenames := failureReportAttachmentFilenames(carried)
	if len(filenames) == 0 {
		return "Files this reply carries: none. Do not say a file is attached."
	}
	return "Files this reply carries: " + strings.Join(filenames, ", ") + ". Say a file is attached only for these."
}

func completionReplyOriginalRequest(request AgentTurnRequest) string {
	return firstNonEmptyString(request.ActiveGoal.OriginalInstruction, request.Prompt)
}

func appendObservationAttachments(attachments []toolcontract.FileAttachment, observation turnObservation) []toolcontract.FileAttachment {
	if observation.Failed() || observation.Tool == "browser_screenshot" {
		return attachments
	}
	return appendUniqueAttachments(attachments, observation.Attachments)
}

func hasAttachmentDevicePath(attachments []toolcontract.FileAttachment, devicePath string) bool {
	normalizedDevicePath := strings.TrimSpace(devicePath)
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.DevicePath) == normalizedDevicePath {
			return true
		}
	}
	return false
}
