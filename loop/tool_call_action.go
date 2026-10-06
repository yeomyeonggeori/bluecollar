package loop

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type toolCallActionOutcome struct {
	Result            AgentTurnResult
	ShouldReturn      bool
	WasHandled        bool
	CanYieldToElapsed bool
}

func (agentTurnRunner *AgentTurnRunner) handleToolCallAction(ctx context.Context, taskContext context.Context, taskRunID string, stepID string, iteration int, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	effortContext, cancelEffort := agentTurnRunner.currentEffortContext(ctx, request.EffortStartedAt)
	defer cancelEffort()
	invocationContext, cancelInvocation := agentTurnRunner.toolInvocationContext(taskContext, effortContext, request, actionDocument.ToolName)
	defer cancelInvocation()
	if outcome := agentTurnRunner.rejectMalformedToolCall(taskRunID, stepID, request, state, actionDocument, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	if outcome := agentTurnRunner.rejectRepeatedToolCall(taskRunID, stepID, state, actionDocument, successfulToolCalls, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	recoveryStep, outcome := agentTurnRunner.prepareRecoveryAttempt(ctx, taskRunID, stepID, request, state, actionDocument, stopForNoProgress)
	if outcome.WasHandled {
		return outcome
	}
	if outcome := agentTurnRunner.rejectUnavailableToolCall(taskRunID, stepID, request, state, actionDocument, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	agentTurnRunner.notePlanMissingBeforeStateChange(taskRunID, request, state, actionDocument)
	state.ToolCallCount++
	if state.ToolCallCount > maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations) && !agentTurnRunner.extendBudgetOneLevelOnce(taskRunID, state) {
		result, _ := agentTurnRunner.completeOrStopForLimit(ctx, taskRunID, request, "max_tool_calls", state, iteration)
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusBlocked, "limit stop", "max_tool_calls")
		return toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true, CanYieldToElapsed: result.TaskRun.Status != agentcontract.TaskStatusCompleted}
	}
	state.Observations = agentTurnRunner.sendCheckpointMessage(effortContext, taskRunID, request, actionDocument, state.Observations)
	observationID := nextObservationIDForObservations(state.Observations)
	observation := agentTurnRunner.invokeTool(invocationContext, request.ToolSet, taskRunID, observationID, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, state.Attachments); isCancelled {
		return toolCallActionOutcome{Result: cancelledResult, ShouldReturn: true, WasHandled: true}
	}
	if isApprovalRequiredObservation(observation) {
		if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
			agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "approval "+actionDocument.ToolName, observation.ContentText())
			return toolCallActionOutcome{Result: pausedResult, ShouldReturn: true, WasHandled: true}
		}
	}
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, recoveryStep)
	agentTurnRunner.applyPlanObservation(effortContext, taskRunID, state, observation)
	updateCompletionIntent(state, actionDocument, observation)
	if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
		agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "continue "+actionDocument.ToolName, observation.ContentText())
		return toolCallActionOutcome{Result: pausedResult, ShouldReturn: true, WasHandled: true}
	}
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "continue "+actionDocument.ToolName, observation.ContentText())
	if !observation.Failed() && observation.RepeatsObservationID != "" && hasPendingObservedSuggestedNextTool(state.Observations) {
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{WasHandled: true}
}

func noProgressToolCallActionOutcome(result AgentTurnResult, shouldStop bool) toolCallActionOutcome {
	return toolCallActionOutcome{
		Result:            result,
		ShouldReturn:      shouldStop,
		WasHandled:        true,
		CanYieldToElapsed: shouldStop,
	}
}

func updateCompletionIntent(state *agentTaskState, actionDocument turnActionDocument, observation turnObservation) {
	state.CompletionIntentToolName = ""
	if observation.Failed() || actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied || actionDocument.HasRemainingWork {
		return
	}
	state.CompletionIntentToolName = observation.Tool
}
