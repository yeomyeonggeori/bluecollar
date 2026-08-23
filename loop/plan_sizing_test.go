package loop

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func planObservation(level string) turnObservation {
	document, _ := json.Marshal(map[string]any{
		"goal":  "message the family members without a venmo account",
		"level": level,
		"steps": []map[string]string{{"title": "log in to the phone app", "status": "pending"}},
	})
	return turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          toolcontract.PlanUpdateToolName,
		Output:        toolcontract.ToolOutput{Content: string(document), Data: document},
	}
}

func runnerWithLevel(taskLevel TaskLevel) (*AgentTurnRunner, *agentTaskState) {
	profile := TaskLevelProfileForLevel(taskLevel)
	services := newTurnRunnerTestServices(nil, TurnOptions{
		MaxIterationCount: profile.MaxIterationCount,
		MaxToolCallCount:  profile.MaxToolCallCount,
	})
	return services.runner, &agentTaskState{Request: AgentTurnRequest{TaskLevel: taskLevel}}
}

func TestAPlanThatNamesABiggerTaskGetsABiggerBudget(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelMedium)
	mediumToolCalls := runner.options.MaxToolCallCount

	runner.widenPaceForPlannedLevel("task-1", state, TaskLevelHigh)

	if runner.options.MaxToolCallCount != TaskLevelProfileForLevel(TaskLevelHigh).MaxToolCallCount {
		t.Fatalf("a task that turned out to be high has to be allowed to finish, got %d", runner.options.MaxToolCallCount)
	}
	if runner.options.MaxToolCallCount <= mediumToolCalls || state.Request.TaskLevel != TaskLevelHigh {
		t.Fatalf("expected the task to carry its new size, got %d calls at level %q", runner.options.MaxToolCallCount, state.Request.TaskLevel)
	}
}

func TestAPlanCannotShrinkTheBudgetItWasGiven(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelHigh)
	highToolCalls := runner.options.MaxToolCallCount

	runner.widenPaceForPlannedLevel("task-1", state, TaskLevelLow)

	if runner.options.MaxToolCallCount != highToolCalls || state.Request.TaskLevel != TaskLevelHigh {
		t.Fatalf("an under-called plan must not cut work already sized larger, got %d at level %q", runner.options.MaxToolCallCount, state.Request.TaskLevel)
	}
}

func TestAPlanWithNoLevelLeavesTheBudgetAlone(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelMedium)
	mediumToolCalls := runner.options.MaxToolCallCount

	runner.widenPaceForPlannedLevel("task-1", state, "")

	if runner.options.MaxToolCallCount != mediumToolCalls {
		t.Fatalf("a plan that says nothing about size must change nothing, got %d", runner.options.MaxToolCallCount)
	}
}

func TestThePlannedLevelIsReadBackFromThePlanObservation(t *testing.T) {
	document, isPlanUpdate := planUpdateFromObservation(planObservation("high"))

	if !isPlanUpdate || document.Level != TaskLevelHigh {
		t.Fatalf("the size the agent wrote on its plan has to survive the round trip, got %+v", document)
	}
}

func TestRestatingTheSameLevelChangesNothing(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelMedium)
	runner.options.MaxElapsedSecond = 836

	runner.widenPaceForPlannedLevel("task-1", state, TaskLevelMedium)

	if runner.options.MaxElapsedSecond != 836 {
		t.Fatalf("a plan that repeats its size must not quietly retighten the clock, got %d seconds", runner.options.MaxElapsedSecond)
	}
}

func TestTheClockFollowsWhatIterationsActuallyCost(t *testing.T) {
	services := newTurnRunnerTestServices(nil, TurnOptions{TaskLevel: TaskLevelMedium})
	runner := services.runner
	runner.noteModelInUse("a/model")
	fallbackClock := runner.options.MaxElapsedSecond

	runner.recordIterationCost(time.Now().Add(-40 * time.Second))
	runner.refreshElapsedBudget(TaskLevelMedium)

	if runner.options.MaxElapsedSecond <= fallbackClock {
		t.Fatalf("a task whose iterations cost 40 seconds each cannot be held to a clock drawn for cheap ones, got %d against %d", runner.options.MaxElapsedSecond, fallbackClock)
	}
}

func TestTheGrantedClockSurvivesTheNextIteration(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelLow)
	runner.noteModelInUse("a/model")
	runner.recordIterationCost(time.Now().Add(-6 * time.Second))

	if !runner.extendBudgetOneLevelOnce("task-1", state) {
		t.Fatalf("a task sized from its level has one level of extension to give")
	}
	grantedSecond := runner.options.MaxElapsedSecond

	runner.recordIterationCost(time.Now().Add(-6 * time.Second))
	runner.refreshElapsedBudget(state.budgetTaskLevel())

	if runner.options.MaxElapsedSecond < grantedSecond {
		t.Fatalf("the clock a grant handed out cannot be taken back on the next iteration, granted %d and kept %d", grantedSecond, runner.options.MaxElapsedSecond)
	}
}

func TestACallersDeadlineIsNotRedrawnFromMeasuredCost(t *testing.T) {
	runner, _ := runnerWithLevel(TaskLevelMedium)
	runner.noteModelInUse("a/model")
	runner.options.DeadlineSecond = 900
	runner.options.MaxElapsedSecond = 900
	runner.recordIterationCost(time.Now().Add(-40 * time.Second))

	runner.refreshElapsedBudget(TaskLevelMedium)

	if runner.options.MaxElapsedSecond != 900 {
		t.Fatalf("the clock a caller set is the clock, and a level profile does not get to redraw it, got %d against 900", runner.options.MaxElapsedSecond)
	}
}

func TestTheGrantOnlyExistsForACallerThatSetNoDeadline(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelLow)
	runner.options.DeadlineSecond = 900

	if runner.levelIsTheWall() {
		t.Fatalf("a caller that gave a deadline gave the only wall the turn needs")
	}
	if runner.extendBudgetOneLevelOnce("task-1", state) {
		t.Fatalf("there is nothing to extend when the level was never the wall")
	}
}

func TestAPlanForABiggerTaskDoesNotShortenTheCallersDeadline(t *testing.T) {
	runner, state := runnerWithLevel(TaskLevelLow)
	runner.noteModelInUse("a/model")
	runner.options.DeadlineSecond = 891
	runner.options.MaxElapsedSecond = 891

	runner.widenPaceForPlannedLevel("task-1", state, TaskLevelMedium)

	if runner.options.MaxElapsedSecond != 891 {
		t.Fatalf("a plan that names a bigger task cannot come back with a shorter clock than the caller gave, got %d against 891", runner.options.MaxElapsedSecond)
	}
	if runner.options.MaxToolCallCount != TaskLevelProfileForLevel(TaskLevelMedium).MaxToolCallCount {
		t.Fatalf("the counts still pace the turn and still follow the plan, got %d", runner.options.MaxToolCallCount)
	}
}
