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
}

type IntakeOptions struct {
	IsEnabled           bool
	DefaultTaskLevel    TaskLevel
	SkillTaskLevelFloor TaskLevel
}
