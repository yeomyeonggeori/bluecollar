package loop

import (
	"fmt"
	"time"
)

const (
	limitPressureStageWrapUp        = "wrap_up"
	limitPressureStageNarrowPalette = "narrow_palette"

	wrapUpThresholdPercent        = 80
	narrowPaletteThresholdPercent = 92

	minimumRemainingCallEstimate = 1
	maximumRemainingCallEstimate = 5
	defaultRemainingCallEstimate = 2
)

type limitPressureWarning struct {
	Stage       string
	Observation *turnObservation
	EventBody   map[string]any
}

func (agentTurnRunner *AgentTurnRunner) nextLimitPressureWarning(state agentTaskState, usedIterationCount int, usedToolCallCount int, elapsed time.Duration, observationIndex int, sentWarnings map[string]bool) *limitPressureWarning {
	if sentWarnings[limitPressureStageNarrowPalette] {
		return nil
	}
	if agentTurnRunner.options.MaxIterationCount < 10 && agentTurnRunner.options.MaxToolCallCount < 5 {
		return nil
	}
	limits := agentTurnRunner.reachableLimits(state)
	stage := limitPressureStageFor(usedIterationCount, usedToolCallCount, elapsed, limits)
	if stage == "" || sentWarnings[stage] {
		return nil
	}
	maxToolCallCount := limits.MaxToolCallCount
	maximumWorkDuration := limits.MaxWorkDuration
	remainingCallEstimate := estimateRemainingToolCallCount(elapsed, maximumWorkDuration, usedToolCallCount)
	warning := &limitPressureWarning{
		Stage: stage,
		EventBody: map[string]any{
			"stage":                 stage,
			"remainingCallEstimate": remainingCallEstimate,
			"taskLevel":             agentTurnRunner.options.TaskLevel,
			"usedIterationCount":    usedIterationCount,
			"usedToolCallCount":     usedToolCallCount,
			"maxIterationCount":     limits.MaxIterationCount,
			"maxToolCallCount":      maxToolCallCount,
			"elapsedSeconds":        int(elapsed.Seconds()),
			"maxElapsedSeconds":     agentTurnRunner.options.MaxElapsedSecond,
			"maxWorkSeconds":        int(maximumWorkDuration.Seconds()),
		},
	}
	if stage == limitPressureStageWrapUp {
		message := wrapUpPressureMessage(remainingCallEstimate)
		observation := newContentObservation(nextObservationID(observationIndex), "limit_pressure", "", message)
		warning.Observation = &observation
	}
	return warning
}

type reachableLimits struct {
	MaxIterationCount int
	MaxToolCallCount  int
	MaxWorkDuration   time.Duration
}

func (agentTurnRunner *AgentTurnRunner) reachableLimits(state agentTaskState) reachableLimits {
	held := reachableLimits{
		MaxIterationCount: agentTurnRunner.options.MaxIterationCount,
		MaxToolCallCount:  maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations),
		MaxWorkDuration:   agentTurnRunner.maximumWorkDuration(),
	}
	if state.didExtendBudgetOneLevel() || !budgetCameFromTheLevel(agentTurnRunner.options, state.Request.TaskLevel) {
		return held
	}
	grantedLevel, hasNextLevel := nextTaskLevel(state.Request.TaskLevel)
	if !hasNextLevel {
		return held
	}
	grantedProfile := TaskLevelProfileForLevel(grantedLevel)
	return reachableLimits{
		MaxIterationCount: grantedProfile.MaxIterationCount,
		MaxToolCallCount:  grantedProfile.MaxToolCallCount,
		MaxWorkDuration:   workDurationWithinTotal(time.Duration(agentTurnRunner.elapsedSecondForGrant(grantedProfile)) * time.Second),
	}
}

func limitPressureStageFor(usedIterationCount int, usedToolCallCount int, elapsed time.Duration, limits reachableLimits) string {
	if limitUsageReached(usedIterationCount, limits.MaxIterationCount, narrowPaletteThresholdPercent) || limitUsageReached(usedToolCallCount, limits.MaxToolCallCount, narrowPaletteThresholdPercent) || elapsedUsageReached(elapsed, limits.MaxWorkDuration, narrowPaletteThresholdPercent) {
		return limitPressureStageNarrowPalette
	}
	if limitUsageReached(usedIterationCount, limits.MaxIterationCount, wrapUpThresholdPercent) || limitUsageReached(usedToolCallCount, limits.MaxToolCallCount, wrapUpThresholdPercent) || elapsedUsageReached(elapsed, limits.MaxWorkDuration, wrapUpThresholdPercent) {
		return limitPressureStageWrapUp
	}
	return ""
}

func elapsedUsageReached(elapsed time.Duration, maxElapsed time.Duration, thresholdPercent int) bool {
	if maxElapsed <= 0 || elapsed <= 0 {
		return false
	}
	return elapsed*100 >= maxElapsed*time.Duration(thresholdPercent)
}

func limitUsageReached(usedCount int, maxCount int, thresholdPercent int) bool {
	if maxCount <= 0 || usedCount <= 0 {
		return false
	}
	return usedCount*100 >= maxCount*thresholdPercent
}

func estimateRemainingToolCallCount(elapsed time.Duration, maxElapsed time.Duration, completedToolCallCount int) int {
	if elapsed <= 0 || maxElapsed <= 0 || completedToolCallCount <= 0 {
		return defaultRemainingCallEstimate
	}
	remainingDuration := maxElapsed - elapsed
	if remainingDuration <= 0 {
		return minimumRemainingCallEstimate
	}
	averageActionDuration := elapsed / time.Duration(completedToolCallCount)
	if averageActionDuration <= 0 {
		return defaultRemainingCallEstimate
	}
	estimate := int(remainingDuration / averageActionDuration)
	if estimate < minimumRemainingCallEstimate {
		return minimumRemainingCallEstimate
	}
	if estimate > maximumRemainingCallEstimate {
		return maximumRemainingCallEstimate
	}
	return estimate
}

func wrapUpPressureMessage(remainingCallEstimate int) string {
	return fmt.Sprintf(
		"Budget check: roughly %d more tool calls fit in the remaining budget. Choose the shortest path to completion now: if a recorded successful observation already satisfies the request, send a final reply citing it; otherwise make the single most essential tool call, then send the final reply. Do not start new exploration or re-verify work that is already recorded.",
		remainingCallEstimate,
	)
}
