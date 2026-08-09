package loop

import "testing"

func TestTaskLevelProfileMapping(t *testing.T) {
	profile := TaskLevelProfileForLevel(TaskLevelMedium)

	if profile.TaskLevel != TaskLevelMedium {
		t.Fatalf("expected medium profile, got %+v", profile)
	}
}

func TestEachTierIsWorthEscalatingTo(t *testing.T) {
	workingLevels := []TaskLevel{TaskLevelLow, TaskLevelMedium, TaskLevelHigh, TaskLevelXHigh, TaskLevelMax}

	for index := 1; index < len(workingLevels); index++ {
		previous := TaskLevelProfileForLevel(workingLevels[index-1])
		current := TaskLevelProfileForLevel(workingLevels[index])
		if current.MaxToolCallCount < previous.MaxToolCallCount*2 || current.MaxIterationCount < previous.MaxIterationCount*2 {
			t.Fatalf("escalating from %s to %s buys %d tool calls instead of %d: a step that small is a ceiling wearing a ladder's clothes",
				previous.TaskLevel, current.TaskLevel, current.MaxToolCallCount-previous.MaxToolCallCount, previous.MaxToolCallCount)
		}
	}
}

func TestTheFirstWorkingTierIsSizedFromRunsThatSucceeded(t *testing.T) {
	const measuredSuccessfulToolCallPercentile95 = 13

	profile := TaskLevelProfileForLevel(TaskLevelLow)

	if profile.MaxToolCallCount != measuredSuccessfulToolCallPercentile95 {
		t.Fatalf("the first budget is the 95th percentile of runs that succeeded, re-derived by bench/derive-budgets; got %d, expected %d",
			profile.MaxToolCallCount, measuredSuccessfulToolCallPercentile95)
	}
}
