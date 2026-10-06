package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (run *turnRun) finish(iteration int, stepID string, iterationRequest AgentTurnRequest, actionDocument turnActionDocument) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	deliveredDocument, delivery, isDelivered := run.runner.deliverReplyAttachments(run.workContext, taskRunID, iterationRequest, &run.state, run.successfulToolCalls, actionDocument)
	if !isDelivered {
		run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "reply", lastObservationText(run.state.Observations))
		return run.stopIfNoProgress(iteration, stepID)
	}
	gateResult := run.runner.validateCompletionGateWithChanges(run.workContext, taskRunID, run.request, run.state.Observations, deliveredDocument)
	run.runner.appendValidityReview(taskRunID, "finish", gateResult.ValidityState)
	if !gateResult.IsSatisfied {
		return run.rejectUnmetCompletion(iteration, stepID, deliveredDocument, gateResult)
	}
	return run.deliverFinalReply(iteration, stepID, deliveredDocument, gateResult, delivery.ReplyNotes)
}

func (run *turnRun) rejectUnmetCompletion(iteration int, stepID string, actionDocument turnActionDocument, gateResult completionGateResult) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	if candidateReply := finishActionMessage(actionDocument); canDeliverBestEffortOnUnmetChanges(run.workContext, gateResult, candidateReply) {
		run.runner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionStateBestEffort, marshalEventBody(map[string]string{"reason": gateResult.Message}))
		return finishedWith(run.runner.completeTaskRunBestEffort(run.workContext, taskRunID, stepID, "finish", run.request, run.state.Observations, gateResult, candidateReply), nil)
	}
	observation := completionGateObservation(len(run.state.Observations)+1, gateResult, run.state.Request.ToolSet, run.state.Observations)
	observation = withCompletionGateRecoveryPacket(observation, gateResult)
	run.state.Observations = append(run.state.Observations, observation)
	run.runner.appendEvent(taskRunID, completionGateEventName(observation), marshalEventBody(observation))
	if observation.Action == "evidence_missing" {
		run.runner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionRequired, marshalEventBody(observation))
	}
	run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, observation.Action, observation.ContentText())
	return run.stopIfNoProgress(iteration, stepID)
}

func (run *turnRun) deliverFinalReply(iteration int, stepID string, actionDocument turnActionDocument, gateResult completionGateResult, replyNotes []string) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	run.runner.appendQualityReview(taskRunID, run.state.QualityCriteria, actionDocument.QualityReview, run.state.Observations)
	reply := finishActionMessage(actionDocument)
	if reply == "" {
		run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusFailed, "finish", "empty final reply message")
		return run.failOrFinalize("empty final reply message", iteration)
	}
	reply, carried := run.runner.replyForFinish(run.workContext, taskRunID, run.request, &run.state, gateResult, reply, replyNotes)
	reply = run.runner.prepareFinishMessageForPlatform(run.workContext, run.request, reply)
	if cancelledResult, isCancelled := run.runner.cancelledTaskResult(taskRunID, run.state.Attachments); isCancelled {
		return finishedWith(cancelledResult, nil)
	}
	if elapsedOutcome := run.stopIfElapsed(iteration); elapsedOutcome.isFinished {
		return elapsedOutcome
	}
	run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "finish", reply)
	result := run.runner.finishedTurnResult(taskRunID, reply, carried)
	result.RecoveryActions = recoveryActionsFromObservations(run.state.Observations)
	return finishedWith(result, nil)
}

func (run *turnRun) fail(iteration int, stepID string, actionDocument turnActionDocument) turnOutcome {
	if _, hasFailureDebt := activeFailureDebt(run.state.Observations); hasFailureDebt {
		if outcome, isHandled := run.checkFailureReport(iteration, stepID, actionDocument); isHandled {
			return outcome
		}
	}
	reason := firstNonEmptyString(actionDocument.Reason, "agent reported failure")
	run.runner.saveStep(run.taskRun.TaskRunID, stepID, agentcontract.TaskStatusFailed, "fail", reason)
	return run.failOrFinalize(reason, iteration)
}

func (run *turnRun) checkFailureReport(iteration int, stepID string, actionDocument turnActionDocument) (turnOutcome, bool) {
	taskRunID := run.taskRun.TaskRunID
	facts := buildFailureReportFacts(run.state.Observations, run.runner.options.RecoveryBudget)
	failureReportResult := validateFailureReportAction(actionDocument, facts)
	if !failureReportResult.IsSatisfied {
		observation := completionGateObservation(len(run.state.Observations)+1, failureReportResult, run.state.Request.ToolSet, run.state.Observations)
		run.state.Observations = append(run.state.Observations, observation)
		run.runner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReportRejected, marshalEventBody(observation))
		run.runner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "failure_report_rejected", observation.ContentText())
		return run.stopIfNoProgress(iteration, stepID), true
	}
	if strings.TrimSpace(actionDocument.Message) != "" {
		if result, isHandled, _ := run.runner.failTerminalNoToolsFailure(taskRunID, stepID, run.request, &run.state, actionDocument); isHandled {
			return finishedWith(result, nil), true
		}
	}
	run.runner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReportFactsUsed, marshalEventBody(actionDocument.UsedFailureFacts))
	return keepGoing, false
}
