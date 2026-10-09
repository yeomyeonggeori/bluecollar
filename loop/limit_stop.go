package loop

import (
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	elapsedReplySchemaName       = "bluecollar_elapsed_reply"
	elapsedReplyRepairSchemaName = "bluecollar_elapsed_reply_repair"
)

func (agentTurnRunner *AgentTurnRunner) completeOrStopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, state *agentTaskState, usedIterationCount int) (AgentTurnResult, error) {
	if result, isCompleted := agentTurnRunner.completeAtStop(ctx, taskRunID, request, state); isCompleted {
		return result, nil
	}
	return agentTurnRunner.stopForLimit(ctx, taskRunID, request, reason, state.Observations, filesAtLimit(*state), state.ExecutionState, usedIterationCount, state.ToolCallCount)
}

func (agentTurnRunner *AgentTurnRunner) stopForElapsedLimitIfReached(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, usedIterationCount int) (AgentTurnResult, bool, error) {
	if ctx.Err() != nil || !agentTurnRunner.currentEffortElapsed(ctx, request.EffortStartedAt) {
		return AgentTurnResult{}, false, nil
	}
	if agentTurnRunner.options.ElapsedBudgetSource != ElapsedBudgetFromCaller && agentTurnRunner.extendBudgetOneLevelOnce(taskRunID, state) {
		return AgentTurnResult{}, false, nil
	}
	result, errorValue := agentTurnRunner.stopAtElapsedLimit(ctx, taskRunID, request, state, usedIterationCount)
	return result, true, errorValue
}

func (agentTurnRunner *AgentTurnRunner) stopAtElapsedLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, usedIterationCount int) (AgentTurnResult, error) {
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentLimitStop, marshalEventBody(agentTurnRunner.limitStopEventBody("max_elapsed", state.Observations, state.Attachments, usedIterationCount, state.ToolCallCount)))
	closingContext, cancelClosing := agentTurnRunner.elapsedClosingContext(ctx, request.EffortStartedAt)
	result, isCompleted := agentTurnRunner.completeAtStop(closingContext, taskRunID, request, state)
	cancelClosing()
	if isCompleted {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentLimitCompletedFromEvidence, marshalEventBody(map[string]string{
			"reason": "max_elapsed",
			"source": "expected_changes",
		}))
		return result, nil
	}
	return agentTurnRunner.blockAtElapsedLimit(ctx, taskRunID, request, state.Observations, filesAtLimit(*state), state.ExecutionState)
}

func (agentTurnRunner *AgentTurnRunner) blockAtElapsedLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, observations []turnObservation, files limitFiles, executionState ExecutionState) (AgentTurnResult, error) {
	taskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusBlocked, "max_elapsed")
	reply, replyStatus := agentTurnRunner.generateElapsedClosingReply(ctx, request, observations, files, executionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentLimitReply, marshalEventBody(replyStatus))
	if ctx.Err() != nil {
		return AgentTurnResult{
			TaskRun:                taskRun,
			ReplySuppressed:        true,
			ReplySuppressionReason: "elapsed closing cancelled",
			RecoveryActions:        recoveryActionsFromObservations(observations),
		}, nil
	}
	if replyStatus.Source == "raw_error" {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "limit", "max_elapsed", replyStatus)
	}
	taskRun = persistTaskRunResult(agentTurnRunner.taskRunService, taskRun, reply)
	failureNotice := FailureNotice{
		Message:           reply,
		Source:            replyStatus.Source,
		Language:          ResolveResponseLanguage(request.ResponseLanguage),
		DiagnosticEventID: diagnosticEventID(request, taskRunID, "limit"),
		IsSendable:        strings.TrimSpace(reply) != "",
	}
	return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, FailureNotice: failureNotice, Attachments: files.Carried, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
}

func (agentTurnRunner *AgentTurnRunner) generateElapsedClosingReply(ctx context.Context, request AgentTurnRequest, observations []turnObservation, files limitFiles, executionState ExecutionState) (string, limitReplyStatus) {
	report := limitFailureReport(request, "", "max_elapsed", observations, files, executionState, recoveryDecision{})
	chatCompleter, isAvailable := model.ResolveTextChatCompleter(agentTurnRunner.languageModel)
	if !isAvailable {
		return buildElapsedLimitRawErrorFailureNotice(request).SendableMessage(), limitReplyStatus{Source: "raw_error", Reason: "chat_unavailable"}
	}
	closingContext, cancelClosing := agentTurnRunner.elapsedClosingContext(ctx, request.EffortStartedAt)
	defer cancelClosing()
	reply, errorValue := elapsedClosingText(closingContext, chatCompleter, elapsedReplySchemaName, buildFailureNoticePrompt(report))
	if errorValue == nil && failureNoticeMessageIsSendable(reply) {
		return reply, limitReplyStatus{Source: "generated"}
	}
	if errorValue == nil {
		repairedReply, repairError := elapsedClosingText(closingContext, chatCompleter, elapsedReplyRepairSchemaName, buildFailureNoticeRepairPrompt(report, reply, 1))
		if repairError == nil && failureNoticeMessageIsSendable(repairedReply) {
			return repairedReply, limitReplyStatus{Source: "generated_repair", FirstInvalid: true, RepairCount: 1}
		}
		errorValue = firstError(repairError, errors.New("the generated notice did not fit a notice"))
	}
	return buildElapsedLimitRawErrorFailureNotice(request).SendableMessage(), limitReplyStatus{
		Source:            "raw_error",
		Reason:            "chat_failed",
		TextRecoveryError: errorString(errorValue),
	}
}

func elapsedClosingText(closingContext context.Context, chatCompleter model.ChatCompleter, schemaName string, prompt string) (string, error) {
	response, errorValue := chatCompleter.GenerateChatCompletion(closingContext, model.ChatCompletionRequest{
		SchemaName: schemaName,
		Messages: []model.ChatCompletionMessage{{
			Role:    "user",
			Content: prompt,
		}},
	})
	if errorValue != nil {
		return "", errorValue
	}
	return model.RecoveryChatCompletionText(response)
}

func firstError(errorValues ...error) error {
	for _, errorValue := range errorValues {
		if errorValue != nil {
			return errorValue
		}
	}
	return nil
}

func completionRawReply(request AgentTurnRequest) string {
	if ResolveResponseLanguage(request.ResponseLanguage) == ResponseLanguageKorean {
		return "요청한 결과는 기록됐지만 최종 답변을 생성하지 못했습니다."
	}
	return "The requested result was recorded, but the final response could not be generated."
}

func (agentTurnRunner *AgentTurnRunner) replyFinalizationContext(parentContext context.Context, request AgentTurnRequest) (context.Context, context.CancelFunc) {
	return recoveryFinalizationContextWithParent(parentContext, request)
}

func (agentTurnRunner *AgentTurnRunner) pauseForLimit(taskRunID string, reason string, observations []turnObservation, attachments []toolcontract.FileAttachment, usedIterationCount int, usedToolCallCount int) agentcontract.TaskRun {
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentLimitStop, marshalEventBody(agentTurnRunner.limitStopEventBody(reason, observations, attachments, usedIterationCount, usedToolCallCount)))
	blockedTaskRun, _ := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusBlocked, reason)
	return blockedTaskRun
}

func (agentTurnRunner *AgentTurnRunner) limitStopEventBody(reason string, observations []turnObservation, attachments []toolcontract.FileAttachment, usedIterationCount int, usedToolCallCount int) map[string]any {
	return map[string]any{
		"taskLevel":          agentTurnRunner.options.TaskLevel,
		"maxIterationCount":  agentTurnRunner.options.MaxIterationCount,
		"maxElapsedSecond":   agentTurnRunner.options.MaxElapsedSecond,
		"maxToolCallCount":   agentTurnRunner.options.MaxToolCallCount,
		"usedIterationCount": usedIterationCount,
		"usedToolCallCount":  usedToolCallCount,
		"limitStopReason":    reason,
		"attachmentCount":    len(attachments),
		"observationCount":   len(observations),
		"actionCounts":       observationActionCounts(observations),
		"toolCounts":         observationToolCounts(observations),
		"recentObservations": recentProgressObservations(observations),
	}
}

func (agentTurnRunner *AgentTurnRunner) stopForLimit(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, files limitFiles, executionState ExecutionState, usedIterationCount int, usedToolCallCount int) (AgentTurnResult, error) {
	blockedTaskRun := agentTurnRunner.pauseForLimit(taskRunID, reason, observations, files.staged(), usedIterationCount, usedToolCallCount)
	failureNotice, replyStatus, hasReply := agentTurnRunner.generateLimitReachedNotice(ctx, taskRunID, request, reason, observations, files, executionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentLimitReply, marshalEventBody(replyStatus))
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "limit", reason, replyStatus)
		failureReport := limitFailureReport(request, taskRunID, reason, observations, files, executionState, recoveryDecision{})
		failureNotice = buildRawErrorFailureNotice(failureReport)
		if reason == "max_elapsed" {
			failureNotice = buildElapsedLimitRawErrorFailureNotice(request)
		}
		fallbackReply := failureNotice.SendableMessage()
		blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, fallbackReply)
		return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: fallbackReply, FailureNotice: failureNotice, Attachments: files.Carried, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
	}
	reply := failureNotice.SendableMessage()
	blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: reply, FailureNotice: failureNotice, Attachments: files.Carried, RecoveryActions: recoveryActionsFromObservations(observations)}, nil
}
