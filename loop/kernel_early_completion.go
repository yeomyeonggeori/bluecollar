package loop

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func (agentKernel *AgentKernel) CompleteLaunchFailure(responseContext context.Context, request AgentTurnRequest, phase string, stepName string, errorValue error) AgentTurnResult {
	taskRun, createError := agentKernel.taskRunForLaunchFailure(request)
	reason := firstNonEmptyString(errorString(errorValue), errorString(createError))
	if createError != nil {
		reason = strings.TrimSpace(reason + "; task_run_create=" + createError.Error())
	}
	failedTaskRun, failError := agentKernel.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError != nil {
		taskRun.Status = agentcontract.TaskStatusFailed
		taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
		failedTaskRun = taskRun
	}
	launchFailureReport := FailureReport{
		Phase:              phase,
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  diagnosticEventID(request, taskRun.TaskRunID, phase),
	}
	failureNotice, noticeStatus := (FailureNoticeGenerator{LanguageModel: agentKernel.languageModel}).Generate(responseContext, launchFailureReport)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(noticeStatus))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReport, marshalEventBody(failureReportEventBody(phase, launchFailureReport, noticeStatus)))
	failedTaskRun = persistTaskRunResult(agentKernel.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: failedTaskRun.Result, FailureNotice: failureNotice, ToolNames: toolNamesForEvent(request.ToolSet)}
}

func (agentKernel *AgentKernel) taskRunForLaunchFailure(request AgentTurnRequest) (agentcontract.TaskRun, error) {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentKernel.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun, nil
		}
	}
	return agentKernel.taskRunService.CreateTaskRunWithOriginAndError(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func launchFailureRequest(request AgentRequest) AgentTurnRequest {
	return AgentTurnRequest{
		RequesterPersonID:   request.RequesterPersonID,
		AgentIdentity:       request.AgentIdentity,
		SourceReference:     request.SourceReference,
		ExistingTaskRunID:   request.ExistingTaskRunID,
		OriginReplyTargetID: request.OriginReplyTargetID,
		OriginIsThread:      request.OriginIsThread,
		ConversationID:      request.ConversationID,
		Prompt:              request.Prompt,
		ResponseLanguage:    request.ResponseLanguage,
		ToolSet:             request.ToolSet,
	}
}

func (agentKernel *AgentKernel) completeTurnRouterFailure(responseContext context.Context, request AgentRequest, errorValue error, routerCallRecords []llmCallRecord) AgentTurnResult {
	result := agentKernel.CompleteLaunchFailure(responseContext, AgentTurnRequest{
		RequesterPersonID:   request.RequesterPersonID,
		SourceReference:     request.SourceReference,
		ExistingTaskRunID:   request.ExistingTaskRunID,
		OriginReplyTargetID: request.OriginReplyTargetID,
		OriginIsThread:      request.OriginIsThread,
		ConversationID:      request.ConversationID,
		Prompt:              request.Prompt,
		ResponseLanguage:    request.ResponseLanguage,
		ToolSet:             request.ToolSet,
	}, "routing", "turn_router", errorValue)
	agentKernel.appendTurnRouterCallRecords(result.TaskRun.TaskRunID, routerCallRecords)
	return result
}

func (agentKernel *AgentKernel) completeConsumedRequest(request AgentRequest, decision TurnDecision, routerCallRecords []llmCallRecord) (AgentTurnResult, error) {
	taskRun := agentKernel.taskRunForRequest(request)
	agentKernel.appendTurnRouterCallRecords(taskRun.TaskRunID, routerCallRecords)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentIntake, marshalEventBody(decision.IntakeDecision()))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentConsumed, marshalEventBody(map[string]string{
		"route":  string(decision.Route),
		"reason": strings.TrimSpace(decision.Reason),
	}))
	completedTaskRun, errorValue := agentKernel.taskRunService.CompleteTaskRun(taskRun.TaskRunID, "consumed")
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	return AgentTurnResult{TaskRun: completedTaskRun, TurnRoute: TurnRouteConsume, FinishMessage: strings.TrimSpace(decision.UserFacingReply), ReplySuppressed: true, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

func (agentKernel *AgentKernel) completeIntakeOnlyRequest(responseContext context.Context, routing turnclassification.Routing, request AgentRequest, intakeDecision IntakeDecision, status agentcontract.TaskStatus, routerCallRecords []llmCallRecord) (AgentTurnResult, error) {
	taskRun := agentKernel.taskRunForRequest(request)
	agentKernel.appendTurnRouterCallRecords(taskRun.TaskRunID, routerCallRecords)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentIntake, marshalEventBody(intakeDecision))
	finishMessage := intakeOnlyFinishMessage(intakeDecision)
	if finishMessage == "" {
		finishMessage = (FailureNoticeGenerator{LanguageModel: agentKernel.languageModel}).GenerateIntakeNotice(responseContext, IntakeReport{
			Classification:    intakeDecision.Classification,
			Reason:            intakeDecision.Reason,
			OriginalRequest:   request.Prompt,
			ResponseLanguage:  request.ResponseLanguage,
			DiagnosticEventID: taskRun.TaskRunID + ":task_intake",
		}).SendableMessage()
	}
	blockedTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, status, intakeDecision.Reason)
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	if status == agentcontract.TaskStatusWaitingUserInput && intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentInputRequested, marshalEventBody(agentcontract.NewAskInputRequest(finishMessage, intakeDecision.ClarificationOptions, request.ResponseLanguage)))
	}
	agentKernel.appendGoalLifecycleEvent(blockedTaskRun, activeGoalFromIntakeOnly(routing, taskRun.TaskRunID, request, intakeDecision, status))
	blockedTaskRun = persistTaskRunResult(agentKernel.taskRunService, blockedTaskRun, finishMessage)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: finishMessage, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

func intakeOnlyFinishMessage(intakeDecision IntakeDecision) string {
	userFacingReply := strings.TrimSpace(intakeDecision.UserFacingReply)
	if intakeDecision.Classification != IntakeClassificationNeedsConfirmation {
		return userFacingReply
	}
	return firstNonEmptyString(strings.TrimSpace(intakeDecision.ClarificationQuestion), userFacingReply)
}
