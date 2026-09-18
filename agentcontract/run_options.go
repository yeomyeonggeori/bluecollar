package agentcontract

import "github.com/yeomyeonggeori/bluecollar/model"

const DefaultReactionEmojiName = "white_check_mark"

type RecoveryBudget struct {
	CorrectedRetry int
	AlternateRoute int
	AdjacentTool   int
	NoToolFallback int
}

const (
	ElapsedBudgetFromCaller = "caller"
	ElapsedBudgetFromLevel  = "level"
)

type TurnOptions struct {
	MaxIterationCount        int
	MaxToolCallCount         int
	MaxElapsedSecond         int
	ElapsedBudgetSource      string
	DeadlineSecond           int
	ContextWindowTokens      int
	RecoveryAttemptLimit     int
	RecoveryBudget           RecoveryBudget
	TaskLevel                TaskLevel
	GenerationOptions        model.GenerationOptions
	DelegationLimit          int
	SystemInstructionOverlay func(AgentTurnRequest) string
}

type IntakeOptions struct {
	IsEnabled           bool
	DefaultTaskLevel    TaskLevel
	SkillTaskLevelFloor TaskLevel
}
