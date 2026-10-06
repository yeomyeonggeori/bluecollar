package loop

import (
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func defaultRecoveryBudget() RecoveryBudget {
	return RecoveryBudget{
		CorrectedRetry: 1,
		AlternateRoute: 1,
		AdjacentTool:   2,
		NoToolFallback: 1,
	}
}

func normalizeRecoveryBudget(budget RecoveryBudget) RecoveryBudget {
	if budget.CorrectedRetry < 0 {
		budget.CorrectedRetry = 0
	}
	if budget.AlternateRoute < 0 {
		budget.AlternateRoute = 0
	}
	if budget.AdjacentTool < 0 {
		budget.AdjacentTool = 0
	}
	if budget.NoToolFallback < 0 {
		budget.NoToolFallback = 0
	}
	return budget
}

func recoveryBudgetIsUnset(budget RecoveryBudget) bool {
	return budget.CorrectedRetry == 0 && budget.AlternateRoute == 0 && budget.AdjacentTool == 0 && budget.NoToolFallback == 0
}

func recoveryToolBudgetTotal(budget RecoveryBudget) int {
	budget = normalizeRecoveryBudget(budget)
	return budget.CorrectedRetry + budget.AlternateRoute + budget.AdjacentTool
}

func recoveryStepSpendsBudget(recoveryStep string) bool {
	switch strings.TrimSpace(recoveryStep) {
	case recoveryStepInspection, recoveryStepIndependentWork:
		return false
	default:
		return true
	}
}

func recoveryBudgetAllowsStep(observations []turnObservation, budget RecoveryBudget, recoveryStep string, attemptKey string) bool {
	budget = normalizeRecoveryBudget(budget)
	switch recoveryStep {
	case recoveryStepCorrectedRetry:
		return correctedRetryUseCount(observations, attemptKey) < budget.CorrectedRetry
	case recoveryStepAlternateRoute:
		return recoveryStepUseCount(observations, recoveryStepAlternateRoute) < budget.AlternateRoute
	case recoveryStepAdjacentTool:
		return recoveryStepUseCount(observations, recoveryStepAdjacentTool) < budget.AdjacentTool
	case recoveryStepInspection, recoveryStepIndependentWork:
		return true
	default:
		return false
	}
}

func correctedRetryUseCount(observations []turnObservation, attemptKey string) int {
	count := 0
	for _, observation := range recoveryEpisodeObservations(observations) {
		if !observation.RecoveryAttemptSpent || strings.TrimSpace(observation.RecoveryStep) != recoveryStepCorrectedRetry {
			continue
		}
		if attemptKey == "" || strings.TrimSpace(observation.RecoveryAttemptKey) == attemptKey {
			count++
		}
	}
	return count
}

func recoveryStepUseCount(observations []turnObservation, recoveryStep string) int {
	count := 0
	for _, observation := range recoveryEpisodeObservations(observations) {
		if observation.RecoveryAttemptSpent && strings.TrimSpace(observation.RecoveryStep) == recoveryStep {
			count++
		}
	}
	return count
}

func maxToolCallCountWithRecovery(options TurnOptions, observations []turnObservation) int {
	if _, hasFailureDebt := activeFailureDebt(observations); !hasFailureDebt {
		return options.MaxToolCallCount
	}
	return options.MaxToolCallCount + recoveryToolBudgetTotal(options.RecoveryBudget)
}

func recoveryBudgetExhaustedObservation(toolSet *toolcontract.ToolSet, index int, failedObservation turnObservation, recoveryStep string, refusedToolName string, originalInstruction string) turnObservation {
	content := "The recovery budget for " + strings.TrimSpace(recoveryStep) + " is exhausted. Use another route only when evidence supports it, answer without tools using failureResolution=no_tool_fallback if enough context exists, or return fail when no evidence-backed recovery is available."
	observation := recoveryGuidanceObservation(toolSet, index, failedObservation, originalInstruction)
	observation.Action = "policy"
	observation = withObservationContent(observation, content+" "+observation.ContentText())
	observation.Summary = observation.ContentText()
	observation.Tool = firstNonEmptyString(strings.TrimSpace(refusedToolName), observation.Tool)
	observation.RecoveryStep = strings.TrimSpace(recoveryStep)
	observation.RecoveryAttemptSpent = false
	observation.PolicyCode = "recovery_budget_exhausted"
	return observation
}

func recoveryToolBudgetExhaustedForRequest(observations []turnObservation, toolSet *toolcontract.ToolSet, budget RecoveryBudget, failureDebt FailureDebt) bool {
	if failureRecoveryIsTerminal(failureDebt.LatestFailure) {
		return true
	}
	budget = normalizeRecoveryBudget(budget)
	if toolAvailableForAction(toolSet, failureDebt.LatestFailure.Tool) && recoveryStepUseCount(observations, recoveryStepCorrectedRetry) < budget.CorrectedRetry {
		return false
	}
	if alternateRouteToolIsAvailable(toolSet, failureDebt.LatestFailure.Tool) {
		if recoveryStepUseCount(observations, recoveryStepAlternateRoute) < budget.AlternateRoute {
			return false
		}
	}
	if adjacentRecoveryToolIsAvailable(toolSet, failureDebt.LatestFailure.Tool) {
		if recoveryStepUseCount(observations, recoveryStepAdjacentTool) < budget.AdjacentTool {
			return false
		}
	}
	return true
}

func failureRecoveryIsTerminal(observation turnObservation) bool {
	return observation.Failure != nil && observation.Failure.Kind == toolcontract.FailureInteractionRequired
}

func alternateRouteToolIsAvailable(toolSet *toolcontract.ToolSet, failedToolName string) bool {
	if toolSet == nil {
		return false
	}
	normalizedFailedToolName := strings.TrimSpace(failedToolName)
	for _, toolName := range toolSet.ListToolNames() {
		if strings.TrimSpace(toolName) != "" && strings.TrimSpace(toolName) != normalizedFailedToolName && isAlternateRouteToolPair(toolSet, normalizedFailedToolName, toolName) {
			return true
		}
	}
	return false
}

func adjacentRecoveryToolIsAvailable(toolSet *toolcontract.ToolSet, failedToolName string) bool {
	if toolSet == nil {
		return false
	}
	normalizedFailedToolName := strings.TrimSpace(failedToolName)
	for _, toolName := range toolSet.ListToolNames() {
		normalizedToolName := strings.TrimSpace(toolName)
		if normalizedToolName != "" && normalizedToolName != normalizedFailedToolName && !isAlternateRouteToolPair(toolSet, normalizedFailedToolName, normalizedToolName) {
			return true
		}
	}
	return false
}
