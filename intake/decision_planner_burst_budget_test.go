package intake

import (
	"strings"
	"testing"
)

func TestFitsBurstBudgetHoldsTheRequestToItsOwnByteBudget(t *testing.T) {
	planner := DecisionPlanner{}
	emptyPromptByteCount := decisionRequestByteCount(buildIntakeDecisionRequest(addressedDecisionRequest("")))
	largestFittingPromptLength := burstDecisionRequestByteBudget - emptyPromptByteCount

	if !planner.FitsBurstBudget(addressedDecisionRequest("hello")) {
		t.Fatal("expected a short message to fit the burst budget")
	}
	if !planner.FitsBurstBudget(addressedDecisionRequest(strings.Repeat("a", largestFittingPromptLength))) {
		t.Fatalf("expected a request of exactly %d bytes to fit", burstDecisionRequestByteBudget)
	}
	if planner.FitsBurstBudget(addressedDecisionRequest(strings.Repeat("a", largestFittingPromptLength+1))) {
		t.Fatalf("expected a request of %d bytes to exceed the budget", burstDecisionRequestByteBudget+1)
	}
}
