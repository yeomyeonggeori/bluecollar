package loop

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const maxStallRecoveryDirectivesPerEpisode = 4

func (agentTurnRunner *AgentTurnRunner) continueStalledRecoveryIfAllowed(taskRunID string, state *agentTaskState, tracker *actionProgressTracker, allowance recoveryAllowance) bool {
	if !allowance.CanRecover {
		return false
	}
	failureDebt, hasFailureDebt := activeFailureDebt(state.Observations)
	if !hasFailureDebt {
		return false
	}
	if !stalledOnRedundantInspection(state.Observations) {
		return false
	}
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	directive := stalledRecoveryDirectiveObservation(nextObservationIDForObservations(state.Observations), failureDebt)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallRecoveryDirective, marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func (agentTurnRunner *AgentTurnRunner) steerStalledTurnTowardNextTool(taskRunID string, state *agentTaskState, tracker *actionProgressTracker) bool {
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	suggestion, isFound := latestObservedSuggestedNextTool(state.Observations)
	if !isFound {
		return false
	}
	if state.Request.ToolSet != nil && !state.Request.ToolSet.IsAllowed(suggestion.ToolName) {
		return false
	}
	directive := suggestedNextToolDirectiveObservation(nextObservationIDForObservations(state.Observations), suggestion)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentSuggestedNextToolDirective, marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func (agentTurnRunner *AgentTurnRunner) steerStalledTurnTowardExit(taskRunID string, state *agentTaskState, tracker *actionProgressTracker) bool {
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	directive := stalledExitDirectiveObservation(nextObservationIDForObservations(state.Observations), state.Observations)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallExitDirective, marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func suggestedNextToolDirectiveObservation(observationID string, suggestion observedSuggestedNextTool) turnObservation {
	message := suggestion.Reason + " Call " + suggestion.ToolName + " now before repeating inspection, asking the user, or closing the task."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"suggestedTool":       suggestion.ToolName,
		"sourceTool":          suggestion.SourceTool,
		"sourceObservationID": suggestion.ObservationID,
	}))
	observation.Summary = message
	return observation
}

func stalledExitDirectiveObservation(observationID string, observations []turnObservation) turnObservation {
	failedTool := ""
	if failureDebt, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt {
		failedTool = strings.TrimSpace(failureDebt.LatestFailure.Tool)
	}
	message := "You are repeating actions without making progress. Stop retrying the same thing and stop re-emitting a final reply that keeps getting rejected. Take one of two exits now: either take a genuinely different action that changes workspace, tool, or evidence state; or, if you cannot obtain what you need because a tool keeps failing or the required evidence is unavailable, end immediately with fail and failureResolution=failure_report, giving the user a short honest explanation of what you could not do. Do not loop and do not ask the user how to proceed."
	missingOperationName := latestMissingRequiredEvidenceOperationName(observations)
	if missingOperationName != "" {
		message = "You have not yet called " + missingOperationName + ". Call that direct tool with the appropriate input before attempting to close again. If it is genuinely not needed for this request, end with fail and failureResolution=failure_report, explaining why in the user reply. Do not re-emit a final reply again without this evidence."
	}
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":                message,
		"failedTool":               failedTool,
		"missingEvidenceOperation": missingOperationName,
	}))
	observation.Summary = message
	return observation
}

func latestMissingRequiredEvidenceOperationName(observations []turnObservation) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "evidence_missing" || observation.RecoveryPacket == nil {
			continue
		}
		if len(observation.RecoveryPacket.AllowedTools) > 0 {
			return strings.TrimSpace(observation.RecoveryPacket.AllowedTools[0])
		}
	}
	return ""
}

func stalledOnRedundantInspection(observations []turnObservation) bool {
	if len(observations) == 0 {
		return false
	}
	lastObservation := observations[len(observations)-1]
	if lastObservation.Action != "policy" || lastObservation.Tool != "file_read" {
		return false
	}
	document := map[string]any{}
	if json.Unmarshal(lastObservation.Output.Data, &document) != nil {
		return false
	}
	return stringValue(document["cacheStatus"]) == "hit"
}

func stalledRecoveryDirectiveObservation(observationID string, failureDebt FailureDebt) turnObservation {
	failedTool := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	message := "You are repeating actions without progress while " + failedTool + " is still failing. You already have the information you need. Make one concrete fix now by editing the offending file with edit, then re-run " + failedTool + ". Do not read the same content again and do not ask the user how to proceed."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"failedTool":          failedTool,
		"failedObservationID": failureDebt.LatestFailure.ObservationID,
	}))
	observation.Summary = message
	return observation
}

func (agentTurnRunner *AgentTurnRunner) shouldPauseForStalledRecovery(taskRunID string, observations []turnObservation) bool {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return false
	}
	if failureClassForObservation(failureDebt.LatestFailure) != failureClassUserInput {
		return false
	}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventAgentNoProgressLoopPaused {
			return false
		}
	}
	return true
}

func (agentTurnRunner *AgentTurnRunner) pauseTurnForStall(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, reason string, progressEvaluation actionProgressEvaluation, allowance recoveryAllowance, state agentTaskState) (AgentTurnResult, bool) {
	notice, replyStatus, hasReply := agentTurnRunner.generateStallPauseNotice(ctx, taskRunID, request, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallPauseReply, marshalEventBody(replyStatus))
	if !hasReply {
		return AgentTurnResult{}, false
	}
	pausedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingUserInput, reason)
	if errorValue != nil {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentNoProgressLoopPaused, marshalEventBody(map[string]any{
		"reason":             reason,
		"progressEvaluation": progressEvaluation,
		"recoveryAllowance":  allowance,
	}))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentGoalWaitingUserInput, marshalEventBody(stalledWaitingGoal(taskRunID, request)))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusWaitingUserInput, "no_progress_loop_paused", reason)
	reply := notice.SendableMessage()
	pausedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, pausedTaskRun, reply)
	return AgentTurnResult{TaskRun: pausedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
}

func (agentTurnRunner *AgentTurnRunner) blockTurnForStall(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, reason string, progressEvaluation actionProgressEvaluation, allowance recoveryAllowance, state agentTaskState) (AgentTurnResult, bool) {
	notice, replyStatus, hasReply := agentTurnRunner.generateStallPauseNotice(ctx, taskRunID, request, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallBlockedReply, marshalEventBody(replyStatus))
	blockedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusBlocked, reason)
	if errorValue != nil {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentNoProgressLoopStopped, marshalEventBody(map[string]any{
		"reason":             reason,
		"progressEvaluation": progressEvaluation,
		"recoveryAllowance":  allowance,
	}))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentGoalBlocked, marshalEventBody(blockedGoal(taskRunID, request, reason)))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusBlocked, "no_progress_loop_stopped", reason)
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "stall", reason, replyStatus)
		failureReport := buildFailureReport(request, taskRunID, "stall", reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState, recoveryDecision{})
		notice = buildRawErrorFailureNotice(failureReport)
		fallbackReply := notice.SendableMessage()
		blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, fallbackReply)
		return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: fallbackReply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
	}
	reply := notice.SendableMessage()
	blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
}

func stalledWaitingGoal(taskRunID string, request AgentTurnRequest) ActiveGoal {
	waitingGoal := request.ActiveGoal
	waitingGoal.GoalID = firstNonEmptyString(waitingGoal.GoalID, taskRunID)
	waitingGoal.TaskRunID = firstNonEmptyString(waitingGoal.TaskRunID, taskRunID)
	waitingGoal.OriginalInstruction = firstNonEmptyString(waitingGoal.OriginalInstruction, request.Prompt)
	waitingGoal.Status = ActiveGoalStatusWaitingUserInput
	return waitingGoal
}

func blockedGoal(taskRunID string, request AgentTurnRequest, reason string) ActiveGoal {
	blockedGoal := request.ActiveGoal
	blockedGoal.GoalID = firstNonEmptyString(blockedGoal.GoalID, taskRunID)
	blockedGoal.TaskRunID = firstNonEmptyString(blockedGoal.TaskRunID, taskRunID)
	blockedGoal.OriginalInstruction = firstNonEmptyString(blockedGoal.OriginalInstruction, request.Prompt)
	blockedGoal.CurrentObjective = firstNonEmptyString(blockedGoal.CurrentObjective, reason)
	blockedGoal.Status = ActiveGoalStatusBlocked
	return blockedGoal
}
