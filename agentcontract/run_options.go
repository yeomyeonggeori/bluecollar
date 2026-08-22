package agentcontract

import "github.com/yeomyeonggeori/bluecollar/model"

const DefaultReactionEmojiName = "white_check_mark"

type RecoveryBudget struct {
	CorrectedRetry int
	AlternateRoute int
	AdjacentTool   int
	NoToolFallback int
}

type TurnOptions struct {
	MaxIterationCount    int
	MaxToolCallCount     int
	MaxElapsedSecond     int
	ContextWindowTokens  int
	RecoveryAttemptLimit int
	RecoveryBudget       RecoveryBudget
	TaskLevel            TaskLevel
	GenerationOptions    model.GenerationOptions
	// Zero keeps delegation off, and off costs a turn nothing: the delegate action is
	// absent from the schema and the instruction says nothing about it.
	DelegationLimit int
	// The base instruction has one version. A host whose provider laddered the turn onto a
	// different model states what that model needs here, so nothing forks the base to say it.
	SystemInstructionOverlay func(AgentTurnRequest) string
}

type IntakeOptions struct {
	IsEnabled           bool
	DefaultTaskLevel    TaskLevel
	SkillTaskLevelFloor TaskLevel
}
