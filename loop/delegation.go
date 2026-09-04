package loop

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func delegationIsAllowed(options TurnOptions) bool {
	return options.DelegationLimit > 0
}

func delegationsRemaining(options TurnOptions, observations []turnObservation) int {
	remaining := options.DelegationLimit
	for _, observation := range observations {
		if observation.Action == "delegate" {
			remaining--
		}
	}
	return remaining
}

// A child is a turn like any other: the host executes its tool calls as the same actor,
// so it reaches nothing its parent could not. What it does not get is the right to
// delegate again, which is what keeps one request from becoming a tree nobody sized.
func childTurnRequest(request AgentTurnRequest, actionDocument turnActionDocument) AgentTurnRequest {
	childRequest := request
	childRequest.Prompt = childPrompt(actionDocument)
	childRequest.InputParts = nil
	childRequest.CarriedOutCalls = nil
	childRequest.PrecomputedTurnDecision = nil
	childRequest.IsApprovalContinuation = false
	childRequest.IsRuntimeRestartResume = false
	childRequest.ActiveGoal = ActiveGoal{OriginalInstruction: strings.TrimSpace(actionDocument.Instruction)}
	childRequest.OutcomeContract = OutcomeContract{}
	return childRequest
}

func childPrompt(actionDocument turnActionDocument) string {
	prompt := strings.TrimSpace(actionDocument.Instruction)
	if expectedResult := strings.TrimSpace(actionDocument.ExpectedResult); expectedResult != "" {
		prompt += "\n\nWhat the caller expects back: " + expectedResult
	}
	return prompt
}

func (agentTurnRunner *AgentTurnRunner) childRunner(state *agentTaskState) *AgentTurnRunner {
	childRunner := *agentTurnRunner
	childRunner.options.DelegationLimit = 0
	childRunner.options.MaxIterationCount = remainingCount(agentTurnRunner.options.MaxIterationCount, state.IterationCount)
	childRunner.options.MaxToolCallCount = remainingCount(agentTurnRunner.options.MaxToolCallCount, state.ToolCallCount)
	childRunner.options.MaxElapsedSecond = remainingCount(agentTurnRunner.options.MaxElapsedSecond, elapsedSecondsSince(state.TurnStartedAt))
	return &childRunner
}

func remainingCount(ceiling int, spent int) int {
	if remaining := ceiling - spent; remaining > 1 {
		return remaining
	}
	return 1
}

func elapsedSecondsSince(startedAt time.Time) int {
	if startedAt.IsZero() {
		return 0
	}
	return int(time.Since(startedAt).Seconds())
}

func (agentTurnRunner *AgentTurnRunner) runDelegatedTurn(ctx context.Context, taskRunID string, state *agentTaskState, actionDocument turnActionDocument) turnObservation {
	observationID := nextObservationIDForObservations(state.Observations)
	instruction := strings.TrimSpace(actionDocument.Instruction)
	if instruction == "" {
		return newFailureObservation(observationID, "delegate", "", "a delegated turn needs an instruction of its own",
			toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "delegate")
	}
	if delegationsRemaining(agentTurnRunner.options, state.Observations) <= 0 {
		return newFailureObservation(observationID, "delegate", "",
			"this task has spent all "+strconv.Itoa(agentTurnRunner.options.DelegationLimit)+" of the delegations it is allowed, so the rest of the work happens here",
			toolcontract.FailurePolicyBlocked, toolcontract.FailureCodes.PolicyBlocked, "delegate")
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventDelegateLaunched, marshalEventBody(map[string]any{
		"observationID":  observationID,
		"instruction":    instruction,
		"expectedResult": strings.TrimSpace(actionDocument.ExpectedResult),
	}))
	childResult, errorValue := agentTurnRunner.childRunner(state).RunTurn(toolcontract.WithDelegatedTurn(ctx), childTurnRequest(state.Request, actionDocument))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventDelegateFinished, marshalEventBody(map[string]any{
		"observationID":  observationID,
		"childTaskRunID": childResult.TaskRun.TaskRunID,
		"childStatus":    string(childResult.TaskRun.Status),
	}))
	if errorValue != nil {
		return newFailureObservation(observationID, "delegate", "", "the delegated turn could not run: "+errorValue.Error(),
			toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "delegate")
	}
	childAttachments := agentTurnRunner.artifactsTheChildProduced(childResult)
	state.Attachments = appendUniqueAttachments(state.Attachments, childAttachments)
	if childResult.TaskRun.Status != agentcontract.TaskStatusCompleted {
		return withAttachments(newFailureObservation(observationID, "delegate", "", delegatedFailureText(childResult),
			toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "delegate"), childAttachments)
	}
	return withAttachments(newContentObservation(observationID, "delegate", "", delegatedReplyText(childResult)), childAttachments)
}

func (agentTurnRunner *AgentTurnRunner) artifactsTheChildProduced(childResult AgentTurnResult) []toolcontract.FileAttachment {
	childEvents := agentTurnRunner.taskRunService.ListTaskEvent(childResult.TaskRun.TaskRunID)
	return appendUniqueAttachments(childResult.Attachments, attachmentsFromObservations(observationsFromTaskEvents(childEvents)))
}

func withAttachments(observation turnObservation, attachments []toolcontract.FileAttachment) turnObservation {
	observation.Attachments = append([]toolcontract.FileAttachment{}, attachments...)
	return observation
}

func delegatedReplyText(childResult AgentTurnResult) string {
	reply := firstNonEmptyString(childResult.FinishMessage, childResult.UserNotice)
	if reply == "" {
		return "The delegated turn finished and said nothing."
	}
	return "The delegated turn finished and reported:\n" + strings.TrimSpace(reply)
}

func delegatedFailureText(childResult AgentTurnResult) string {
	text := "The delegated turn ended " + string(childResult.TaskRun.Status) + "."
	if reason := firstNonEmptyString(childResult.TaskRun.FailureReason, childResult.UserNotice, childResult.FinishMessage); reason != "" {
		text += " It reported: " + strings.TrimSpace(reason)
	}
	return text + " Its work is not done, so finish it here or report what is missing."
}
