package loop

import (
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func (run *turnRun) chooseAction(iteration int, stepID string, iterationRequest AgentTurnRequest) (turnActionDocument, bool, turnOutcome) {
	allowQualityCriteria := len(run.state.QualityCriteria) == 0 && outcomeContractNeedsQualityCriteria(iterationRequest.OutcomeContract)
	actionDocument, isBatched := takeBatchedAction(&run.state)
	run.hasIterationSpentModelCall = !isBatched
	if isBatched {
		return actionDocument, true, keepGoing
	}
	actionDocument, actionError := run.runner.nextAction(run.workContext, run.taskRun.TaskRunID, iterationRequest, run.state, allowQualityCriteria)
	if actionError == nil {
		return actionDocument, true, keepGoing
	}
	return actionDocument, false, run.recoverFromActionError(iteration, stepID, actionError)
}

func (run *turnRun) recoverFromActionError(iteration int, stepID string, actionError error) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	if isUnreadableModelActionError(actionError) {
		run.state.Observations = append(run.state.Observations, unreadableActionObservation(run.state.Observations, actionError))
		run.runner.appendEvent(taskRunID, agentcontract.TaskEventAgentUnreadableAction, marshalEventBody(map[string]string{"reason": actionError.Error()}))
		return keepGoing
	}
	run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "agent turn iteration", actionError.Error())
	if errors.Is(actionError, context.Canceled) {
		return run.abandon(actionError, "the model call was cancelled: "+actionError.Error())
	}
	if errors.Is(actionError, context.DeadlineExceeded) {
		return run.recoverFromDeadline(iteration, actionError)
	}
	return run.failOrFinalize("llm action failed: "+actionError.Error(), iteration)
}

func (run *turnRun) recoverFromDeadline(iteration int, actionError error) turnOutcome {
	if run.callerContext.Err() != nil {
		return run.abandon(run.callerContext.Err(), "the turn's caller context ended while the model was answering: "+actionError.Error())
	}
	if !run.runner.currentEffortElapsed(run.request.EffortStartedAt) {
		run.refreshWorkContext()
		return keepGoing
	}
	if run.runner.options.ElapsedBudgetSource != ElapsedBudgetFromCaller && run.runner.extendBudgetOneLevelOnce(run.taskRun.TaskRunID, &run.state) {
		run.refreshWorkContext()
		return keepGoing
	}
	result, errorValue := run.runner.stopAtElapsedLimit(run.taskContext, run.taskRun.TaskRunID, run.request, &run.state, iteration-1)
	return finishedWith(result, errorValue)
}

func (run *turnRun) failOrFinalize(reason string, iteration int) turnOutcome {
	result, errorValue := run.runner.finalizeIfSatisfiedOrFail(run.taskContext, run.request, reason, &run.state, iteration)
	return finishedWith(result, errorValue)
}

func (run *turnRun) recordAction(actionDocument turnActionDocument) {
	if !executionStateIsEmpty(actionDocument.ExecutionStateUpdate) {
		run.state.ExecutionState = normalizeExecutionState(actionDocument.ExecutionStateUpdate)
		run.runner.appendEvent(run.taskRun.TaskRunID, agentcontract.TaskEventAgentExecutionState, marshalEventBody(run.state.ExecutionState))
	}
	run.runner.appendEvent(run.taskRun.TaskRunID, agentcontract.TaskEventAgentAction, marshalEventBody(actionDocument))
}

func (run *turnRun) applyAction(iteration int, stepID string, iterationRequest AgentTurnRequest, actionDocument turnActionDocument) turnOutcome {
	switch strings.TrimSpace(actionDocument.Action) {
	case "set_quality_criteria":
		return run.setQualityCriteria(stepID, actionDocument)
	case "delegate":
		return run.delegate(stepID, actionDocument)
	case "reply":
		return run.reply(iteration, stepID, iterationRequest, actionDocument)
	case "finish":
		return run.finish(iteration, stepID, iterationRequest, actionDocument)
	case "continue":
		return run.callTool(iteration, stepID, iterationRequest, actionDocument)
	case "fail":
		return run.fail(iteration, stepID, actionDocument)
	default:
		return run.rejectUnknownAction(iteration, stepID, actionDocument)
	}
}

func (run *turnRun) setQualityCriteria(stepID string, actionDocument turnActionDocument) turnOutcome {
	run.state.QualityCriteria = normalizeQualityCriteria(actionDocument.QualityCriteria)
	criteria := marshalEventBody(map[string]any{"criteria": run.state.QualityCriteria})
	run.state.Observations = append(run.state.Observations, turnObservation{
		ObservationID: nextObservationIDForObservations(run.state.Observations),
		Action:        "set_quality_criteria",
		Output:        toolcontract.ToolOutput{Content: criteria},
	})
	run.runner.appendEvent(run.taskRun.TaskRunID, agentcontract.TaskEventAgentQualityCriteria, criteria)
	run.runner.saveStep(run.taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "set_quality_criteria", criteria)
	return keepGoing
}

func (run *turnRun) delegate(stepID string, actionDocument turnActionDocument) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	observation := run.runner.runDelegatedTurn(run.workContext, taskRunID, &run.state, actionDocument)
	run.runner.recordToolObservation(taskRunID, &run.state, actionDocument, run.successfulToolCalls, observation, "")
	run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "delegate", observation.ContentText())
	return keepGoing
}

func (run *turnRun) reply(iteration int, stepID string, iterationRequest AgentTurnRequest, actionDocument turnActionDocument) turnOutcome {
	if result, shouldReturn := run.runner.handleReplyAction(run.workContext, run.taskRun.TaskRunID, stepID, iterationRequest, &run.state, run.successfulToolCalls, actionDocument); shouldReturn {
		return finishedWith(result, nil)
	}
	return run.stopIfNoProgress(iteration, stepID)
}

func (run *turnRun) callTool(iteration int, stepID string, iterationRequest AgentTurnRequest, actionDocument turnActionDocument) turnOutcome {
	outcome := run.runner.handleToolCallAction(run.workContext, run.taskContext, run.taskRun.TaskRunID, stepID, iteration, iterationRequest, &run.state, actionDocument, run.successfulToolCalls, run.stopForNoProgress)
	if !outcome.ShouldReturn {
		rememberBatchedActions(&run.state, actionDocument, iterationRequest.ToolSet.ListToolNames(), iterationRequest.ToolExposure)
		return keepGoing
	}
	if outcome.CanYieldToElapsed {
		if elapsedOutcome := run.stopIfElapsed(iteration); elapsedOutcome.isFinished {
			return elapsedOutcome
		}
	}
	return finishedWith(outcome.Result, nil)
}

func (run *turnRun) rejectUnknownAction(iteration int, stepID string, actionDocument turnActionDocument) turnOutcome {
	observation := newFailureObservation(nextObservationIDForObservations(run.state.Observations), "invalid_action", "", "unknown action: "+actionDocument.Action, toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "action_parse")
	run.state.Observations = append(run.state.Observations, observation)
	run.runner.saveStep(run.taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "invalid_action", observation.ContentText())
	return run.stopIfNoProgress(iteration, stepID)
}

func (run *turnRun) stopIfElapsed(iteration int) turnOutcome {
	result, isElapsed, errorValue := run.runner.stopForElapsedLimitIfReached(run.taskContext, run.taskRun.TaskRunID, run.request, &run.state, iteration)
	if !isElapsed {
		return keepGoing
	}
	return finishedWith(result, errorValue)
}

func (run *turnRun) stopIfNoProgress(iteration int, stepID string) turnOutcome {
	result, shouldStop := run.stopForNoProgress(stepID)
	if !shouldStop {
		return keepGoing
	}
	if elapsedOutcome := run.stopIfElapsed(iteration); elapsedOutcome.isFinished {
		return elapsedOutcome
	}
	return finishedWith(result, nil)
}

func (run *turnRun) stopForNoProgress(stepID string) (AgentTurnResult, bool) {
	progressEvaluation := run.progressTracker.evaluate(run.state.Observations)
	if progressEvaluation.HasProgress || !progressEvaluation.shouldStop() {
		return AgentTurnResult{}, false
	}
	taskRunID := run.taskRun.TaskRunID
	recoveryAllowance := evaluateRecoveryAllowance(run.state.Observations, run.runner.options.RecoveryBudget)
	if run.runner.continueStalledRecoveryIfAllowed(taskRunID, &run.state, &run.progressTracker, recoveryAllowance) {
		return AgentTurnResult{}, false
	}
	if run.runner.steerStalledTurnTowardNextTool(taskRunID, &run.state, &run.progressTracker) {
		return AgentTurnResult{}, false
	}
	if run.runner.steerStalledTurnTowardExit(taskRunID, &run.state, &run.progressTracker) {
		return AgentTurnResult{}, false
	}
	reason := "stopped after repeated model actions without workspace, tool, artifact, attachment, or new failure progress, including after stall guidance"
	if run.runner.shouldPauseForStalledRecovery(taskRunID, run.state.Observations) {
		if result, isPaused := run.runner.pauseTurnForStall(run.workContext, taskRunID, stepID, run.request, reason, progressEvaluation, recoveryAllowance, run.state); isPaused {
			return result, true
		}
	}
	return run.runner.blockTurnForStall(run.workContext, taskRunID, stepID, run.request, reason, progressEvaluation, recoveryAllowance, run.state)
}
