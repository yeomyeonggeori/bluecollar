package agentcontract

import (
	"time"
)

type TaskLevelProfile struct {
	CostCeiling       time.Duration
	TaskLevel         TaskLevel
	Duration          time.Duration
	MaxIterationCount int
	MaxToolCallCount  int
}

const (
	measuredSuccessfulIterationPercentile95 = 20
	measuredSuccessfulToolCallPercentile95  = 13
	answerWithoutToolsIterationCount        = 4
	answerWithoutToolsToolCallCount         = 1

	unmeasuredOutputTokensPerSecond  = 20
	measuredOutputTokensPerModelCall = 205
	localCostPerModelCall            = 200 * time.Millisecond
	durationMargin                   = 2
	firstTierCostCeiling             = 15 * time.Minute
	answerWithoutToolsCostCeiling    = 2 * time.Minute
	fastestPlausibleCostPerCall      = time.Second
)

func costCeilingForDoublings(doublings int) time.Duration {
	return firstTierCostCeiling << doublings
}

func escalatedFrom(firstTierBudget int, doublings int) int {
	return firstTierBudget << doublings
}

func profileForTier(taskLevel TaskLevel, doublings int) TaskLevelProfile {
	iterationCount := escalatedFrom(measuredSuccessfulIterationPercentile95, doublings)
	return TaskLevelProfile{
		TaskLevel:         taskLevel,
		CostCeiling:       costCeilingForDoublings(doublings),
		Duration:          DurationForIterationCount(iterationCount, IterationCost{}, costCeilingForDoublings(doublings)),
		MaxIterationCount: iterationCount,
		MaxToolCallCount:  escalatedFrom(measuredSuccessfulToolCallPercentile95, doublings),
	}
}

var taskLevelProfiles = []TaskLevelProfile{
	{
		TaskLevel:         TaskLevelXLow,
		CostCeiling:       answerWithoutToolsCostCeiling,
		Duration:          DurationForIterationCount(answerWithoutToolsIterationCount, IterationCost{}, answerWithoutToolsCostCeiling),
		MaxIterationCount: answerWithoutToolsIterationCount,
		MaxToolCallCount:  answerWithoutToolsToolCallCount,
	},
	profileForTier(TaskLevelLow, 0),
	profileForTier(TaskLevelMedium, 1),
	profileForTier(TaskLevelHigh, 2),
	profileForTier(TaskLevelXHigh, 3),
	profileForTier(TaskLevelMax, 4),
}

func TaskLevelProfileForLevel(taskLevel TaskLevel) TaskLevelProfile {
	normalizedTaskLevel := NormalizeTaskLevel(string(taskLevel))
	if normalizedTaskLevel == "" {
		normalizedTaskLevel = TaskLevelLow
	}
	for _, taskLevelProfile := range taskLevelProfiles {
		if taskLevelProfile.TaskLevel == normalizedTaskLevel {
			return taskLevelProfile
		}
	}
	return taskLevelProfiles[1]
}

func NextTaskLevel(taskLevel TaskLevel) (TaskLevel, bool) {
	currentRank := TaskLevelRank(TaskLevelProfileForLevel(taskLevel).TaskLevel)
	if currentRank < 0 || currentRank+1 >= len(taskLevelProfiles) {
		return "", false
	}
	return taskLevelProfiles[currentRank+1].TaskLevel, true
}

func TaskLevelWantsProgressCheckpoints(taskLevel TaskLevel) bool {
	return TaskLevelRank(taskLevel) >= TaskLevelRank(TaskLevelMedium)
}

func TaskLevelWantsSingleFinalReply(taskLevel TaskLevel) bool {
	return NormalizeTaskLevel(string(taskLevel)) == TaskLevelXLow
}

func TaskLevelRequiresPlan(taskLevel TaskLevel) bool {
	return TaskLevelRank(taskLevel) >= TaskLevelRank(TaskLevelMedium)
}
