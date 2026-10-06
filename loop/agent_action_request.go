package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	refusalsThatWithdrawFinish = 2
	refusalsThatOfferTheExit   = 3
)

func BuildAgentActionRequest(state agentTaskState) model.StructuredResponseRequest {
	return buildAgentActionRequest(state, true, false)
}

func buildAgentActionRequestCarryingToolResultsNatively(state agentTaskState) model.StructuredResponseRequest {
	return buildAgentActionRequest(state, false, true)
}

func buildAgentActionRequest(state agentTaskState, includeToolDescription bool, toolResultsCarriedNatively bool) model.StructuredResponseRequest {
	allowQualityCriteria := len(state.QualityCriteria) == 0
	modelToolSet := modelCallableToolSet(state.Request.ToolSet, state.Request.RestrictActionToTerminalOnly)
	failureFacts := buildFailureReportFacts(state.Observations, state.Options.RecoveryBudget)
	hasFailureDebt := len(failureFacts.Attempts) > 0
	allowFail := shouldExposeFailAction(state)
	allowFinish := shouldExposeFinishAction(state)
	toolDescription := ""
	if includeToolDescription {
		toolDescription = buildAgentToolDescription(modelToolSet)
	}
	messages := (PromptAssembler{}).buildTurnMessages(
		state.Request,
		state.Observations,
		state.systemInstructionText(),
		toolDescription,
		toolResultsCarriedNatively,
		state.ExecutionState,
	)
	if hasFailureDebt {
		messages = append(messages, model.Message{
			Role:    "system",
			Content: failureDebtActionContractMessage(failureFacts),
		})
	}
	return model.StructuredResponseRequest{
		Messages: messages,
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:               "bluecollar_agent_turn_action",
			Document:           actionSchemaForToolSet(modelToolSet, citableEvidenceIDs(state.Observations), allowQualityCriteria, hasFailureDebt, allowFail, allowFinish, delegationIsAllowed(state.Options)),
			IsStrictlyEnforced: true,
		},
		GenerationOptions: state.Options.GenerationOptions,
	}
}

func modelCallableToolSet(toolSet *toolcontract.ToolSet, restrictToTerminalActionsOnly bool) *toolcontract.ToolSet {
	if restrictToTerminalActionsOnly {
		return nil
	}
	return toolSet
}

func shouldExposeFailAction(state agentTaskState) bool {
	if completionGateHasRefusedEnough(state.Observations) {
		return true
	}
	if declinedToClaimSuccess(state.Observations) {
		return true
	}
	if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
		return true
	}
	return turnIsAlreadyWrappingUp(state)
}

func declinedToClaimSuccess(observations []turnObservation) bool {
	for _, observation := range observations {
		if observation.PolicyCode == policyCodeGoalNotClaimedSatisfied {
			return true
		}
	}
	return false
}

func completionGateHasRefusedEnough(observations []turnObservation) bool {
	return completionRefusalCount(observations) >= refusalsThatOfferTheExit
}

func completionRefusalCount(observations []turnObservation) int {
	count := 0
	for _, observation := range observations {
		if observation.Action == "evidence_missing" {
			count++
		}
	}
	return count
}

func turnIsAlreadyWrappingUp(state agentTaskState) bool {
	return limitUsageReached(state.IterationCount, state.Options.MaxIterationCount, wrapUpThresholdPercent) ||
		limitUsageReached(state.ToolCallCount, state.Options.MaxToolCallCount, wrapUpThresholdPercent)
}

func finishKeepsBeingRefusedWithNothingDoneBetween(observations []turnObservation) bool {
	refusalCount := 0
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !observation.Failed() && strings.TrimSpace(observation.Tool) != "" {
			break
		}
		if observation.Action == "evidence_missing" {
			refusalCount++
		}
	}
	return refusalCount >= refusalsThatWithdrawFinish
}

func finishWasRejectedWithoutAnyToolEvidence(observations []turnObservation) bool {
	if len(observations) == 0 {
		return false
	}
	latestObservation := observations[len(observations)-1]
	if latestObservation.Action != "evidence_missing" {
		return false
	}
	switch latestObservation.PolicyCode {
	case "attachment_missing", "attachment_invalid", "required_tool_missing":
		return true
	}
	for _, observation := range observations {
		if !observation.Failed() && strings.TrimSpace(observation.Tool) != "" {
			return false
		}
	}
	return true
}

func actionSchemaForToolSet(toolSet *toolcontract.ToolSet, citableEvidenceIDs []string, allowQualityCriteria bool, hasFailureDebt bool, allowFailValues ...bool) string {
	return actionSchemaCitingEvidence(toolSet, citableEvidenceIDs, allowQualityCriteria, hasFailureDebt, allowFailValues...)
}

func citableEvidenceIDs(observations []turnObservation) []string {
	evidenceIDs := []string{}
	for _, observation := range observations {
		if observation.Failed() || strings.TrimSpace(observation.Tool) == "" {
			continue
		}
		evidenceIDs = append(evidenceIDs, observation.ObservationID)
	}
	return evidenceIDs
}

func qualityCriteriaForActionRequest(allowQualityCriteria bool) []qualityCriterion {
	if allowQualityCriteria {
		return nil
	}
	return []qualityCriterion{{ID: "existing", Description: "existing criteria"}}
}

func shouldExposeFinishAction(state agentTaskState) bool {
	if completionGateHasRefusedEnough(state.Observations) {
		return true
	}
	if finishWasRejectedWithoutAnyToolEvidence(state.Observations) {
		return false
	}
	return !finishKeepsBeingRefusedWithNothingDoneBetween(state.Observations)
}
