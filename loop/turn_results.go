package loop

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) failLaunchStep(ctx context.Context, taskRun agentcontract.TaskRun, request AgentTurnRequest, stepName string, errorValue error) AgentTurnResult {
	reason := errorString(errorValue)
	agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLaunchStepError, marshalEventBody(map[string]string{
		"phase":    "launch",
		"stepName": strings.TrimSpace(stepName),
		"error":    reason,
	}))
	return agentTurnRunner.failTurnWithGeneratedNotice(ctx, taskRun, request, "launch", stepName, reason)
}

func (agentTurnRunner *AgentTurnRunner) failTurnWithGeneratedNotice(ctx context.Context, taskRun agentcontract.TaskRun, request AgentTurnRequest, phase string, stepName string, reason string) AgentTurnResult {
	failedTaskRun := agentTurnRunner.failTaskRunWithReason(taskRun, reason)
	noticeContext, cancelNotice := closingNoticeContextWithParent(ctx, request)
	defer cancelNotice()
	failureNotice, noticeStatus := (FailureNoticeGenerator{LanguageModel: agentTurnRunner.recoveryLanguageModel}).Generate(noticeContext, FailureReport{
		Phase:              phase,
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  diagnosticEventID(request, taskRun.TaskRunID, phase),
	})
	agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(noticeStatus))
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	result := AgentTurnResult{TaskRun: failedTaskRun, UserNotice: failedTaskRun.Result, FailureNotice: failureNotice, ToolNames: toolNamesForEvent(request.ToolSet)}
	if strings.TrimSpace(result.UserNotice) == "" {
		result.ReplySuppressed = true
		result.ReplySuppressionReason = phase + " failure notice could not be written"
	}
	return result
}

func (agentTurnRunner *AgentTurnRunner) pausedTaskResult(taskRunID string, observation turnObservation, attachments []toolcontract.FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !isWaitingForUser(taskRun.Status) {
		return AgentTurnResult{}, false
	}
	if taskRun.Status == agentcontract.TaskStatusWaitingApproval {
		reply := firstNonEmptyString(approvalObservationUserFacingMessage(observation), taskRun.FailureReason)
		if reply == "" {
			agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentApprovalUserFacingMessageMissing, marshalEventBody(observation))
		}
		return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
	}
	reply := firstNonEmptyString(taskRun.FailureReason, toolObservationMessage(observation), observation.ContentText())
	return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
}

func (agentTurnRunner *AgentTurnRunner) cancelledTaskResult(taskRunID string, attachments []toolcontract.FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != agentcontract.TaskStatusCancelled {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventTaskStopOutboxSuppressed, "task run was cancelled before reply delivery")
	return AgentTurnResult{TaskRun: taskRun, ReplySuppressed: true, Attachments: attachments}, true
}

func (agentTurnRunner *AgentTurnRunner) abandonedTurnResult(ctx context.Context, taskRunID string, request AgentTurnRequest, cause error, reason string, attachments []toolcontract.FileAttachment) AgentTurnResult {
	if result, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, attachments); isCancelled {
		return result
	}
	taskRun, _ := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if isTaskRunFinished(taskRun.Status) {
		return AgentTurnResult{TaskRun: taskRun, UserNotice: taskRun.Result, Attachments: attachments}
	}
	isDelegatedTurn := toolcontract.IsDelegatedTurn(ctx)
	isOwnedByCanceller := errors.Is(cause, context.Canceled) && !isDelegatedTurn
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentTurnAbandoned, marshalEventBody(map[string]string{
		"reason":              reason,
		"statusWhenAbandoned": string(taskRun.Status),
		"endsTheTaskRun":      strconv.FormatBool(!isOwnedByCanceller),
	}))
	if isOwnedByCanceller {
		return AgentTurnResult{TaskRun: taskRun, ReplySuppressed: true, ReplySuppressionReason: reason, Attachments: attachments}
	}
	if isDelegatedTurn {
		return AgentTurnResult{
			TaskRun:                agentTurnRunner.failTaskRunWithReason(taskRun, reason),
			ReplySuppressed:        true,
			ReplySuppressionReason: reason,
			Attachments:            attachments,
		}
	}
	result := agentTurnRunner.failTurnWithGeneratedNotice(ctx, taskRun, request, "turn", "run_turn", reason)
	result.Attachments = attachments
	return result
}

func (agentTurnRunner *AgentTurnRunner) failTaskRunWithReason(taskRun agentcontract.TaskRun, reason string) agentcontract.TaskRun {
	failedTaskRun, failError := agentTurnRunner.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError == nil {
		return failedTaskRun
	}
	taskRun.Status = agentcontract.TaskStatusFailed
	taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
	return taskRun
}

func (agentTurnRunner *AgentTurnRunner) finishedTurnResult(taskRunID string, reply string, attachments []toolcontract.FileAttachment) AgentTurnResult {
	completedTaskRun, completionError := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	if completionError == nil {
		return AgentTurnResult{TaskRun: completedTaskRun, FinishMessage: reply, Attachments: attachments}
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionPersistFailed, marshalEventBody(map[string]string{"error": completionError.Error()}))
	if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, attachments); isCancelled {
		return cancelledResult
	}
	blockedTaskRun, pauseError := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusBlocked, completionError.Error())
	if pauseError != nil {
		return AgentTurnResult{
			TaskRun:     agentcontract.TaskRun{TaskRunID: taskRunID, Status: agentcontract.TaskStatusBlocked, FailureReason: completionError.Error(), Result: reply},
			UserNotice:  reply,
			Attachments: attachments,
		}
	}
	return AgentTurnResult{TaskRun: persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply), UserNotice: reply, Attachments: attachments}
}

func isTaskRunFinished(status agentcontract.TaskStatus) bool {
	switch status {
	case agentcontract.TaskStatusCompleted, agentcontract.TaskStatusFailed, agentcontract.TaskStatusCancelled, agentcontract.TaskStatusInterrupted, agentcontract.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func (agentTurnRunner *AgentTurnRunner) finalizeIfSatisfiedOrFail(ctx context.Context, request AgentTurnRequest, reason string, state *agentTaskState, usedIterationCount int) (AgentTurnResult, error) {
	effortContext, cancelEffort := agentTurnRunner.currentEffortContext(ctx, request.EffortStartedAt)
	result, isCompleted := agentTurnRunner.completeAtStop(effortContext, state.TaskRunID, request, state)
	effortError := effortContext.Err()
	cancelEffort()
	if isCompleted {
		return result, nil
	}
	if ctx.Err() != nil {
		return agentTurnRunner.abandonedTurnResult(ctx, state.TaskRunID, request, ctx.Err(), "the turn's caller context ended before the agent could finish: "+errorString(ctx.Err()), state.Attachments), nil
	}
	if errors.Is(effortError, context.DeadlineExceeded) || agentTurnRunner.currentEffortElapsed(request.EffortStartedAt) {
		return agentTurnRunner.stopAtElapsedLimit(ctx, state.TaskRunID, request, state, usedIterationCount)
	}
	return agentTurnRunner.failTurnWithContext(ctx, state.TaskRunID, request, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
}

func (agentTurnRunner *AgentTurnRunner) failTurnWithContext(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []toolcontract.FileAttachment, executionState ExecutionState) (AgentTurnResult, error) {
	failureNotice, replyStatus, hasReply := agentTurnRunner.generateFailureNotice(ctx, taskRunID, request, reason, observations, attachments, executionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(replyStatus))
	reply := failureNotice.SendableMessage()
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "failure", reason, replyStatus)
		failureReport := buildFailureReport(request, taskRunID, "failure", reason, observations, attachments, executionState, recoveryDecision{})
		failureNotice = buildRawErrorFailureNotice(failureReport)
		reply = failureNotice.SendableMessage()
	}
	failedTaskRun, _ := agentTurnRunner.taskRunService.FailTaskRun(taskRunID, reason)
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, reply)
	result := AgentTurnResult{TaskRun: failedTaskRun, UserNotice: reply, FailureNotice: failureNotice, RecoveryActions: recoveryActionsFromObservations(observations)}
	return result, nil
}

func persistTaskRunResult(taskRunService taskstate.TaskRunStore, taskRun agentcontract.TaskRun, result string) agentcontract.TaskRun {
	persistedTaskRun, errorValue := taskRunService.RecordTaskRunResult(taskRun.TaskRunID, result)
	if errorValue != nil {
		taskRun.Result = result
		return taskRun
	}
	return persistedTaskRun
}
