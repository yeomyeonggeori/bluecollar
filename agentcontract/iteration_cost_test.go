package agentcontract

import (
	"testing"
	"time"
)

const testCostCeiling = time.Hour

func recordIterations(observer *IterationCostObserver, modelName string, costs ...time.Duration) {
	for _, cost := range costs {
		observer.Record(modelName, cost)
	}
}

func TestWithNothingMeasuredTheUnmeasuredRateStands(t *testing.T) {
	observer := NewIterationCostObserver()

	unseen := DurationForIterationCount(20, observer.CostForModel("unseen/model"), testCostCeiling)

	if unseen != DurationForIterationCount(20, IterationCost{}, testCostCeiling) {
		t.Fatal("a model nobody has called yet has to be given some clock, and it is the one for a model we have not met")
	}
}

func TestOneCallIsAlreadyBetterThanNoCall(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "fast/model", time.Second)

	oneCall := DurationForIterationCount(20, observer.CostForModel("fast/model"), testCostCeiling)

	if oneCall >= DurationForIterationCount(20, IterationCost{}, testCostCeiling) {
		t.Fatalf("one call is thin evidence but it is evidence, and waiting for a quorum leaves the first task on a stranger's clock: %s", oneCall)
	}
}

func TestASlowModelIsGivenTheTimeItActuallyNeeds(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "slow/model", 30*time.Second, 30*time.Second, 30*time.Second)

	slow := DurationForIterationCount(20, observer.CostForModel("slow/model"), testCostCeiling)

	if slow <= DurationForIterationCount(20, IterationCost{}, testCostCeiling) {
		t.Fatalf("holding slow hardware to a faster machine's clock fails every task it is given, which is not a standard but a guarantee of failure: %s", slow)
	}
}

func TestTheCostCeilingIsWhatBoundsIt(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "hung/model", time.Hour, time.Hour, time.Hour)

	bounded := DurationForIterationCount(20, observer.CostForModel("hung/model"), 15*time.Minute)

	if bounded != 15*time.Minute {
		t.Fatalf("something has to stop a model that answers once an hour, and it is what the requester is willing to spend: %s", bounded)
	}
}

func TestTheEstimateIsTheMedianOfWhateverIsOnHand(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "model", 1*time.Second, 9*time.Second, 2*time.Second)

	if observer.CostForModel("model").CostPerIteration != 2*time.Second {
		t.Fatalf("the median of one, nine and two seconds is two, and a mean would let one slow call move the deadline: %s", observer.CostForModel("model").CostPerIteration)
	}
}

func TestAnOutlierMattersLessAsEvidenceAccumulates(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "model", time.Second)
	afterOne := observer.CostForModel("model").CostPerIteration
	for range 10 {
		recordIterations(observer, "model", time.Second)
	}
	recordIterations(observer, "model", 60*time.Second)

	if observer.CostForModel("model").CostPerIteration != afterOne {
		t.Fatalf("one wild call among twelve should not move a median, got %s against %s", observer.CostForModel("model").CostPerIteration, afterOne)
	}
}

func TestTheWindowStopsGrowing(t *testing.T) {
	observer := NewIterationCostObserver()
	for range iterationCostSampleCeiling + 50 {
		recordIterations(observer, "model", time.Second)
	}

	if observer.CostForModel("model").SampleCount != iterationCostSampleCeiling {
		t.Fatalf("a model measured for months should answer for how it behaves now, got %d samples", observer.CostForModel("model").SampleCount)
	}
}

func TestTheDeadlineFollowsTheModelInUse(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "fast/model", time.Second, time.Second, time.Second)
	recordIterations(observer, "slower/model", 5*time.Second, 5*time.Second, 5*time.Second)

	if observer.CostOfModelInUse().CostPerIteration != 5*time.Second {
		t.Fatal("after a switch the deadline belongs to the model now answering")
	}
}

func TestAnImplausiblyFastMeasurementDoesNotCollapseTheDeadline(t *testing.T) {
	observer := NewIterationCostObserver()
	recordIterations(observer, "instant/model", time.Millisecond, time.Millisecond, time.Millisecond)

	deadline := DurationForIterationCount(20, observer.CostForModel("instant/model"), testCostCeiling)

	if deadline < 20*fastestPlausibleCostPerCall {
		t.Fatalf("a measurement near zero would hand a task a deadline near zero, and it would be blocked before its second call: %s", deadline)
	}
}

func TestAnIterationSpentInToolsCountsTowardTheClock(t *testing.T) {
	modelBound := NewIterationCostObserver()
	recordIterations(modelBound, "a/model", 8*time.Second, 8*time.Second, 8*time.Second)
	toolBound := NewIterationCostObserver()
	recordIterations(toolBound, "a/model", 24*time.Second, 24*time.Second, 24*time.Second)

	modelBoundClock := DurationForIterationCount(40, modelBound.CostForModel("a/model"), testCostCeiling)
	toolBoundClock := DurationForIterationCount(40, toolBound.CostForModel("a/model"), testCostCeiling)

	if toolBoundClock <= modelBoundClock {
		t.Fatalf("a task that spends 78%% of its time running commands needs more clock than one that does not, got %s against %s", toolBoundClock, modelBoundClock)
	}
}
