package loop

import (
	"context"
	"fmt"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const maximumElapsedClosingDuration = time.Minute

func (agentTurnRunner *AgentTurnRunner) setElapsedBudgetFromProfile(taskLevelProfile TaskLevelProfile) {
	agentTurnRunner.options.MaxElapsedSecond = agentTurnRunner.elapsedSecondForProfile(taskLevelProfile)
}

func (agentTurnRunner *AgentTurnRunner) elapsedSecondForProfile(taskLevelProfile TaskLevelProfile) int {
	budgetSecond := int(elapsedBudgetForProfile(taskLevelProfile, agentTurnRunner.iterationCostObserver.CostOfModelInUse()).Seconds())
	return agentTurnRunner.withinDeadline(budgetSecond)
}

func (agentTurnRunner *AgentTurnRunner) withinDeadline(budgetSecond int) int {
	if deadlineSecond := agentTurnRunner.options.DeadlineSecond; deadlineSecond > 0 {
		return min(budgetSecond, deadlineSecond)
	}
	return budgetSecond
}

func (agentTurnRunner *AgentTurnRunner) elapsedSecondForGrant(grantedProfile TaskLevelProfile) int {
	profileSecond := agentTurnRunner.elapsedSecondForProfile(grantedProfile)
	if agentTurnRunner.options.MaxElapsedSecond <= 0 || agentTurnRunner.options.MaxIterationCount <= 0 {
		return profileSecond
	}
	proportionalSecond := agentTurnRunner.options.MaxElapsedSecond * grantedProfile.MaxIterationCount / agentTurnRunner.options.MaxIterationCount
	return max(profileSecond, agentTurnRunner.withinDeadline(proportionalSecond))
}

func (agentTurnRunner *AgentTurnRunner) refreshElapsedBudget(taskLevel TaskLevel) {
	if agentTurnRunner.iterationCostObserver.CostOfModelInUse().CostPerIteration <= 0 {
		return
	}
	budgetBeforeRefresh := agentTurnRunner.options.MaxElapsedSecond
	agentTurnRunner.setElapsedBudgetFromProfile(TaskLevelProfileForLevel(taskLevel))
	if budgetBeforeRefresh > 0 && agentTurnRunner.options.MaxElapsedSecond < budgetBeforeRefresh {
		agentTurnRunner.options.MaxElapsedSecond = budgetBeforeRefresh
	}
}

func budgetCameFromTheLevel(options TurnOptions, taskLevel TaskLevel) bool {
	levelProfile := TaskLevelProfileForLevel(taskLevel)
	return options.MaxToolCallCount == levelProfile.MaxToolCallCount && options.MaxIterationCount == levelProfile.MaxIterationCount
}

func grantedBudgetObservation(observations []turnObservation, options TurnOptions) turnObservation {
	return newContentObservation(
		nextObservationIDForObservations(observations),
		"policy",
		"",
		fmt.Sprintf("Budget update: this task was resized and now has %d tool calls and %d steps in total. Any earlier budget check quoted the smaller budget and no longer applies.", options.MaxToolCallCount, options.MaxIterationCount),
	)
}

func (agentTurnRunner *AgentTurnRunner) extendBudgetOneLevelOnce(taskRunID string, state *agentTaskState) bool {
	if state.didExtendBudgetOneLevel() || !budgetCameFromTheLevel(agentTurnRunner.options, state.Request.TaskLevel) {
		return false
	}
	grantedLevel, hasNextLevel := nextTaskLevel(state.Request.TaskLevel)
	if !hasNextLevel {
		return false
	}
	grantedProfile := TaskLevelProfileForLevel(grantedLevel)
	state.GrantedTaskLevel = grantedLevel
	agentTurnRunner.options.MaxElapsedSecond = agentTurnRunner.elapsedSecondForGrant(grantedProfile)
	agentTurnRunner.options.MaxToolCallCount = grantedProfile.MaxToolCallCount
	agentTurnRunner.options.MaxIterationCount = grantedProfile.MaxIterationCount
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentBudgetExtendedOneLevel, marshalEventBody(map[string]any{
		"grantedLevel":      string(grantedLevel),
		"maxToolCallCount":  grantedProfile.MaxToolCallCount,
		"maxIterationCount": grantedProfile.MaxIterationCount,
		"maxElapsedSecond":  agentTurnRunner.options.MaxElapsedSecond,
	}))
	return true
}

func (agentTurnRunner *AgentTurnRunner) currentEffortElapsed(turnStartedAt time.Time) bool {
	if turnStartedAt.IsZero() || agentTurnRunner.options.MaxElapsedSecond <= 0 {
		return false
	}
	return time.Since(turnStartedAt) >= agentTurnRunner.maximumWorkDuration()
}

func (agentTurnRunner *AgentTurnRunner) currentEffortContext(parentContext context.Context, effortStartedAt time.Time) (context.Context, context.CancelFunc) {
	if effortStartedAt.IsZero() || agentTurnRunner.options.MaxElapsedSecond <= 0 {
		return context.WithCancel(parentContext)
	}
	deadline := effortStartedAt.Add(agentTurnRunner.maximumWorkDuration())
	return context.WithDeadline(parentContext, deadline)
}

func (agentTurnRunner *AgentTurnRunner) maximumWorkDuration() time.Duration {
	maximumElapsedDuration := time.Duration(agentTurnRunner.options.MaxElapsedSecond) * time.Second
	return workDurationWithinTotal(maximumElapsedDuration)
}

func workDurationWithinTotal(maximumElapsedDuration time.Duration) time.Duration {
	if maximumElapsedDuration <= 0 {
		return 0
	}
	return maximumElapsedDuration - elapsedClosingDuration(maximumElapsedDuration)
}

func elapsedClosingDuration(maximumElapsedDuration time.Duration) time.Duration {
	if maximumElapsedDuration <= 0 {
		return 0
	}
	closingDuration := maximumElapsedDuration / 3
	if closingDuration > maximumElapsedClosingDuration {
		return maximumElapsedClosingDuration
	}
	return closingDuration
}

func (agentTurnRunner *AgentTurnRunner) elapsedClosingContext(parentContext context.Context, effortStartedAt time.Time) (context.Context, context.CancelFunc) {
	if effortStartedAt.IsZero() || agentTurnRunner.options.MaxElapsedSecond <= 0 {
		return context.WithCancel(parentContext)
	}
	maximumElapsedDuration := time.Duration(agentTurnRunner.options.MaxElapsedSecond) * time.Second
	return context.WithDeadline(parentContext, effortStartedAt.Add(maximumElapsedDuration))
}

func (agentTurnRunner *AgentTurnRunner) turnElapsed(turnStartedAt time.Time) time.Duration {
	if turnStartedAt.IsZero() {
		return 0
	}
	return time.Since(turnStartedAt)
}
