package loop

import (
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/iterationcost"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, taskRunID string, iterationRequest AgentTurnRequest, state agentTaskState, allowQualityCriteria bool) (turnActionDocument, error) {
	actionState := agentTurnRunner.actionStateForIteration(iterationRequest, state, allowQualityCriteria)
	actionState = agentTurnRunner.promptStateForAction(ctx, taskRunID, actionState)
	return agentTurnRunner.decideActionPatiently(ctx, taskRunID, actionState)
}

func (agentTurnRunner *AgentTurnRunner) actionStateForIteration(iterationRequest AgentTurnRequest, state agentTaskState, allowQualityCriteria bool) agentTaskState {
	return agentTaskState{
		Request:           iterationRequest,
		Options:           agentTurnRunner.options,
		Observations:      append([]turnObservation{}, state.Observations...),
		ExecutionState:    state.ExecutionState,
		ContextSummary:    state.ContextSummary,
		QualityCriteria:   qualityCriteriaForActionRequest(allowQualityCriteria),
		SystemInstruction: state.SystemInstruction,
	}
}

func (agentTurnRunner *AgentTurnRunner) decideActionPatiently(ctx context.Context, taskRunID string, state agentTaskState) (turnActionDocument, error) {
	patience, isMeasured := iterationcost.ModelCallPatience(agentTurnRunner.iterationCostObserver.CostOfModelInUse())
	if !isMeasured {
		return DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
	}
	callContext, cancelCall := context.WithTimeout(ctx, patience)
	actionDocument, errorValue := DecideAgentAction(callContext, agentTurnRunner.languageModel, state)
	wasCut := errors.Is(callContext.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancelCall()
	if errorValue == nil || !wasCut {
		return actionDocument, errorValue
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentModelCallCut, marshalEventBody(map[string]any{
		"model":           agentTurnRunner.modelInUse,
		"patienceSeconds": int(patience.Seconds()),
	}))
	return DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
}

func outcomeContractNeedsQualityCriteria(contract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(contract.ArtifactRequirement)
	if artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone {
		return true
	}
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	return expectedResultIncludesType(contract, ExpectedResultTypeFile) ||
		expectedResultIncludesType(contract, ExpectedResultTypeLink)
}
