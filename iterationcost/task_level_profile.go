package iterationcost

import (
	"time"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type TaskLevelProfile struct {
	CostCeiling       time.Duration
	TaskLevel         agentcontract.TaskLevel
	Duration          time.Duration
	MaxIterationCount int
	MaxToolCallCount  int
}

const measuredSuccessfulIterationPercentile95 = 20

const measuredSuccessfulToolCallPercentile95 = 13

const answerWithoutToolsIterationCount = 4

const answerWithoutToolsToolCallCount = 1

const unmeasuredOutputTokensPerSecond = 20

const measuredOutputTokensPerModelCall = 205

const localCostPerModelCall = 200 * time.Millisecond

const durationMargin = 2

const answerWithoutToolsCostCeiling = 2 * time.Minute

const fastestPlausibleCostPerCall = time.Second

const slowestPlausibleCostPerCall = 2 * time.Minute

func costCeilingForDoublings(doublings int) time.Duration {
	iterationCount := escalatedFrom(measuredSuccessfulIterationPercentile95, doublings)
	return time.Duration(iterationCount) * slowestPlausibleCostPerCall * durationMargin
}

func escalatedFrom(firstTierBudget int, doublings int) int {
	return firstTierBudget << doublings
}

func profileForTier(taskLevel agentcontract.TaskLevel, doublings int) TaskLevelProfile {
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
		TaskLevel:         agentcontract.TaskLevelXLow,
		CostCeiling:       answerWithoutToolsCostCeiling,
		Duration:          DurationForIterationCount(answerWithoutToolsIterationCount, IterationCost{}, answerWithoutToolsCostCeiling),
		MaxIterationCount: answerWithoutToolsIterationCount,
		MaxToolCallCount:  answerWithoutToolsToolCallCount,
	},
	profileForTier(agentcontract.TaskLevelLow, 0),
	profileForTier(agentcontract.TaskLevelMedium, 1),
	profileForTier(agentcontract.TaskLevelHigh, 2),
	profileForTier(agentcontract.TaskLevelXHigh, 3),
	profileForTier(agentcontract.TaskLevelMax, 4),
}

func TaskLevelProfileForLevel(taskLevel agentcontract.TaskLevel) TaskLevelProfile {
	normalizedTaskLevel := agentcontract.NormalizeTaskLevel(string(taskLevel))
	if normalizedTaskLevel == "" {
		normalizedTaskLevel = agentcontract.TaskLevelLow
	}
	for _, taskLevelProfile := range taskLevelProfiles {
		if taskLevelProfile.TaskLevel == normalizedTaskLevel {
			return taskLevelProfile
		}
	}
	return taskLevelProfiles[1]
}

func NextTaskLevel(taskLevel agentcontract.TaskLevel) (agentcontract.TaskLevel, bool) {
	currentRank := turnclassification.TaskLevelRank(TaskLevelProfileForLevel(taskLevel).TaskLevel)
	if currentRank < 0 || currentRank+1 >= len(taskLevelProfiles) {
		return "", false
	}
	return taskLevelProfiles[currentRank+1].TaskLevel, true
}

func TaskLevelWantsProgressCheckpoints(taskLevel agentcontract.TaskLevel) bool {
	return turnclassification.TaskLevelRank(taskLevel) >= turnclassification.TaskLevelRank(agentcontract.TaskLevelMedium)
}

func TaskLevelWantsSingleFinalReply(taskLevel agentcontract.TaskLevel) bool {
	return agentcontract.NormalizeTaskLevel(string(taskLevel)) == agentcontract.TaskLevelXLow
}

func TaskLevelRequiresPlan(taskLevel agentcontract.TaskLevel) bool {
	return turnclassification.TaskLevelRank(taskLevel) >= turnclassification.TaskLevelRank(agentcontract.TaskLevelMedium)
}
