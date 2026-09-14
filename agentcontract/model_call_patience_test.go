package agentcontract

import (
	"testing"
	"time"
)

func TestModelCallPatienceIsMeasuredOrAbsent(t *testing.T) {
	if _, isMeasured := ModelCallPatience(IterationCost{}); isMeasured {
		t.Fatal("nothing measured means nothing to cut against")
	}
	patience, isMeasured := ModelCallPatience(IterationCost{CostPerIteration: 7 * time.Second})
	if !isMeasured || patience != 84*time.Second {
		t.Fatalf("expected the p95 of the median with the budget margin, got %v %v", patience, isMeasured)
	}
}
