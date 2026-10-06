package turnoptions

import (
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type RecoveryBudget struct {
	CorrectedRetry int
	AlternateRoute int
	AdjacentTool   int
	NoToolFallback int
}

const ElapsedBudgetFromCaller = "caller"

const ElapsedBudgetFromLevel = "level"

type TurnOptions struct {
	MaxIterationCount        int
	MaxToolCallCount         int
	MaxElapsedSecond         int
	ElapsedBudgetSource      string
	DeadlineSecond           int
	ContextWindowTokens      int
	RecoveryAttemptLimit     int
	RecoveryBudget           RecoveryBudget
	TaskLevel                agentcontract.TaskLevel
	GenerationOptions        model.GenerationOptions
	DelegationLimit          int
	SystemInstructionOverlay func(agentcontract.AgentTurnRequest) string
}
