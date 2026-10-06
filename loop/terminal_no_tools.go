package loop

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) runTerminalNoToolsStep(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, reason string) AgentTurnResult {
	rejectionReason := ""
	for attempt := 1; attempt <= 3; attempt++ {
		actionDocument, actionError := agentTurnRunner.terminalNoToolsAction(ctx, request, state.Observations, state.ExecutionState, rejectionReason)
		if actionError != nil {
			rejectionReason = "terminal no-tools action was invalid: " + actionError.Error()
			agentTurnRunner.recordTerminalNoToolsRejection(taskRunID, stepID, state, rejectionReason)
			continue
		}
		if !executionStateIsEmpty(actionDocument.ExecutionStateUpdate) {
			state.ExecutionState = normalizeExecutionState(actionDocument.ExecutionStateUpdate)
			agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentExecutionState, marshalEventBody(state.ExecutionState))
		}
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentTerminalNoToolsAction, marshalEventBody(actionDocument))
		result, isComplete, validationMessage := agentTurnRunner.applyTerminalNoToolsAction(ctx, taskRunID, stepID, request, state, actionDocument)
		if isComplete {
			return result
		}
		rejectionReason = validationMessage
		agentTurnRunner.recordTerminalNoToolsRejection(taskRunID, stepID, state, rejectionReason)
	}
	progressEvaluation := actionProgressEvaluation{Reason: "terminal no-tools action did not produce a valid final reply or fail"}
	allowance := recoveryAllowance{CanRecover: false, Reason: "tool recovery budget exhausted"}
	result, _ := agentTurnRunner.blockTurnForStall(ctx, taskRunID, stepID, request, reason, progressEvaluation, allowance, *state)
	return result
}

func (agentTurnRunner *AgentTurnRunner) terminalNoToolsAction(ctx context.Context, request AgentTurnRequest, observations []turnObservation, executionState ExecutionState, rejectionReason string) (turnActionDocument, error) {
	messages := agentTurnRunner.buildTurnMessages(request, observations, executionState)
	messages = append(messages, model.Message{
		Role:    "system",
		Content: terminalNoToolsInstruction(observations, agentTurnRunner.options.RecoveryBudget, rejectionReason),
	})
	structuredResponse, errorValue := agentTurnRunner.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "bluecollar_agent_terminal_no_tools_action",
			Document:           terminalNoToolsActionSchema(),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: agentTurnRunner.options.GenerationOptions,
	})
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	return ParseAgentActionResponse(structuredResponse)
}

func terminalNoToolsInstruction(observations []turnObservation, budget RecoveryBudget, rejectionReason string) string {
	facts := buildFailureReportFacts(observations, budget)
	parts := []string{
		"Recovery tool budget is exhausted. Do not call tools and do not select tools.",
		"Return exactly one terminal action.",
		"Use reply with final=true only when the original request is fulfilled from current context with failureResolution=no_tool_fallback. Include the requested answer in the reply itself; claiming it was delivered is not delivery. Required attachments and other requested effects must already be recorded as successful.",
		"Use fail only when completion is blocked, with failureResolution=failure_report and usedFailureFacts copied from FailureReportFacts.",
		"Only the recorded tool calls in FailureReportFacts were attempted. Guidance and model calls are not tool executions. Do not invent retries or infer that a failed response means a write was not saved; report an unverified outcome as uncertain.",
		"FailureReportFacts:\n" + marshalEventBody(facts),
	}
	if strings.TrimSpace(rejectionReason) != "" {
		parts = append(parts, "Previous terminal action was rejected: "+strings.TrimSpace(rejectionReason))
	}
	return strings.Join(parts, "\n")
}

func (agentTurnRunner *AgentTurnRunner) applyTerminalNoToolsAction(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool, string) {
	switch strings.TrimSpace(actionDocument.Action) {
	case "finish":
		return agentTurnRunner.completeTerminalNoToolsFinish(ctx, taskRunID, stepID, request, state, actionDocument)
	case "fail":
		return agentTurnRunner.failTerminalNoToolsFailure(taskRunID, stepID, request, state, actionDocument)
	default:
		return AgentTurnResult{}, false, "terminal no-tools action must be a final reply or fail"
	}
}

func (agentTurnRunner *AgentTurnRunner) completeTerminalNoToolsFinish(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool, string) {
	if !isRecoveredFailureDebtResolution(actionDocument.FailureResolution) {
		return AgentTurnResult{}, false, "a final reply requires failureResolution to be recovered_with_success or no_tool_fallback"
	}
	completionGateResult := agentTurnRunner.validateCompletionGateWithChanges(ctx, taskRunID, request, state.Observations, actionDocument)
	agentTurnRunner.appendValidityReview(taskRunID, "terminal_no_tools_finish", completionGateResult.ValidityState)
	if !completionGateResult.IsSatisfied {
		return AgentTurnResult{}, false, completionGateResult.Message
	}
	agentTurnRunner.appendQualityReview(taskRunID, state.QualityCriteria, actionDocument.QualityReview, state.Observations)
	reply := finishActionMessage(actionDocument)
	if strings.TrimSpace(reply) == "" {
		return AgentTurnResult{}, false, "finish message is empty"
	}
	reply = agentTurnRunner.prepareFinishMessageForPlatform(ctx, request, reply)
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "terminal_no_tools_finish", reply)
	result := agentTurnRunner.finishedTurnResult(taskRunID, reply, completionGateResult.Attachments)
	result.RecoveryActions = recoveryActionsFromObservations(state.Observations)
	return result, true, ""
}

func (agentTurnRunner *AgentTurnRunner) failTerminalNoToolsFailure(taskRunID string, stepID string, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument) (AgentTurnResult, bool, string) {
	if strings.TrimSpace(actionDocument.Reason) == "" && strings.TrimSpace(actionDocument.Message) == "" {
		return AgentTurnResult{}, false, "fail requires a non-empty reason"
	}
	facts := buildFailureReportFacts(state.Observations, agentTurnRunner.options.RecoveryBudget)
	failureReportResult := validateFailureReportAction(actionDocument, facts)
	if !failureReportResult.IsSatisfied {
		return AgentTurnResult{}, false, failureReportResult.Message
	}
	reason := strings.TrimSpace(firstNonEmptyString(actionDocument.Message, actionDocument.Reason, "agent reported failure"))
	notice, failureReport, validationMessage := failureNoticeFromTerminalAction(request, taskRunID, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
	if validationMessage != "" {
		return AgentTurnResult{}, false, validationMessage
	}
	failedTaskRun, _ := agentTurnRunner.taskRunService.FailTaskRun(taskRunID, reason)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReportFactsUsed, marshalEventBody(actionDocument.UsedFailureFacts))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReport, marshalEventBody(failureReportEventBody("terminal_no_tools", failureReport, FailureNoticeGenerationStatus{Source: notice.Source})))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(FailureNoticeGenerationStatus{Source: notice.Source}))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "terminal_no_tools_fail", reason)
	reply := notice.SendableMessage()
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, reply)
	return AgentTurnResult{TaskRun: failedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true, ""
}

func failureNoticeFromTerminalAction(request AgentTurnRequest, taskRunID string, reason string, observations []turnObservation, attachments []toolcontract.FileAttachment, executionState ExecutionState) (FailureNotice, FailureReport, string) {
	decision := recoveryDecision{
		NextAction:      strings.TrimSpace(reason),
		UserReplyIntent: strings.TrimSpace(reason),
	}
	failureReport := buildFailureReport(request, taskRunID, "terminal_no_tools", reason, observations, attachments, executionState, decision)
	notice := buildFailureNotice(reason, "terminal_no_tools", failureReport)
	if notice.IsSendable {
		return notice, failureReport, ""
	}
	return FailureNotice{}, failureReport, "fail.reason must be a safe user-facing explanation"
}

func (agentTurnRunner *AgentTurnRunner) recordTerminalNoToolsRejection(taskRunID string, stepID string, state *agentTaskState, reason string) {
	observation := completionGateObservation(len(state.Observations)+1, completionGateResult{Message: strings.TrimSpace(reason)}, state.Request.ToolSet, state.Observations)
	state.Observations = append(state.Observations, observation)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentTerminalNoToolsRejected, marshalEventBody(observation))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "terminal_no_tools_rejected", observation.ContentText())
}
