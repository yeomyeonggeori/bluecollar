package loop

import "time"

func executionBudgetStartedAt(request AgentRequest) time.Time {
	if request.IsRuntimeRestartResume || !request.ExecutionStartedAt.After(request.TurnStartedAt) {
		return request.TurnStartedAt
	}
	return request.ExecutionStartedAt
}
