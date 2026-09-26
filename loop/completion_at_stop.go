package loop

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func (agentTurnRunner *AgentTurnRunner) completeAtStop(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState) (AgentTurnResult, bool) {
	if !agentTurnRunner.workIsDoneAtStop(ctx, taskRunID, request, *state) {
		return AgentTurnResult{}, false
	}
	completedTaskRun, errorValue := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, "")
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionPersistFailed, marshalEventBody(map[string]string{"error": errorValue.Error()}))
		return AgentTurnResult{}, false
	}
	reply := agentTurnRunner.writeCompletionReply(ctx, taskRunID, request, state.Observations)
	detachedContext, cancelDetached := context.WithTimeout(context.WithoutCancel(ctx), completionPersistenceTimeout)
	defer cancelDetached()
	reply = agentTurnRunner.prepareFinishMessageForPlatform(detachedContext, request, reply)
	agentTurnRunner.saveStep(taskRunID, taskRunID+":completion", agentcontract.TaskStatusCompleted, "completion_at_stop", reply)
	return AgentTurnResult{
		TaskRun:         persistTaskRunResult(agentTurnRunner.taskRunService, completedTaskRun, reply),
		FinishMessage:   reply,
		Attachments:     attachmentsNotYetDelivered(state.Attachments, state.DeliveredAttachmentPaths),
		RecoveryActions: recoveryActionsFromObservations(state.Observations),
	}, true
}

func (agentTurnRunner *AgentTurnRunner) workIsDoneAtStop(ctx context.Context, taskRunID string, request AgentTurnRequest, state agentTaskState) bool {
	if ctx.Err() != nil {
		return false
	}
	if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
		return false
	}
	if !buildAttachmentValidityState(request.WorkspaceRootPath, state.Attachments).Passed {
		return false
	}
	changeResult := agentTurnRunner.evaluateExpectedChanges(ctx, taskRunID, request, state.Observations)
	if !changeResult.IsSatisfied {
		return false
	}
	isDone := changeResult.AreChangesConfirmed || modelDeclaredCompletion(state)
	if isDone {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionStateFinalized, marshalEventBody(map[string]bool{
			"areChangesConfirmed":     changeResult.AreChangesConfirmed,
			"modelDeclaredCompletion": modelDeclaredCompletion(state),
		}))
	}
	return isDone
}

func modelDeclaredCompletion(state agentTaskState) bool {
	return state.CompletionIntentToolName != ""
}

func (agentTurnRunner *AgentTurnRunner) writeCompletionReply(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation) string {
	chatCompleter, isAvailable := model.ResolveTextChatCompleter(agentTurnRunner.languageModel)
	if !isAvailable {
		return completionRawReply(request)
	}
	reply, errorValue := generateCompletionReply(ctx, chatCompleter, request, observations)
	if errorValue == nil {
		return reply
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionReplyFailed, marshalEventBody(map[string]string{"error": errorValue.Error()}))
	return completionRawReply(request)
}
