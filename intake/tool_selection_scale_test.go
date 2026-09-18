package intake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestAThousandToolsAreSelectedFromInBatchesTheModelAccepts(t *testing.T) {
	syntheticTools := syntheticCatalog(30, 50)
	request := addressedDecisionRequest("경쟁사 자료 찾아서 정리해줘")
	request.ToolSet = newProviderToolSet(syntheticTools)
	outcome := startTaskOutcome()
	outcome.TurnDecision.InitialToolNames = nil
	outcome.ToolProbabilities = map[string]float64{"provider_0_tool_0": 0.91, "provider_29_tool_49": 0.77}
	decisionModel := &concurrencyCountingDecisionModel{outcome: outcome}
	callLedger := &agentcontract.IntakeCallLedger{}

	startedAt := time.Now()
	decisions, errorValue := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }).Decide(context.Background(), request, callLedger)

	if errorValue != nil {
		t.Fatalf("expected a catalog this size to be decided: %v", errorValue)
	}
	if time.Since(startedAt) > 10*time.Second {
		t.Fatalf("expected the selection to stay fast, got %s", time.Since(startedAt))
	}
	plan := planToolSelection(request, []string{decisionMessageKey(0)}, resolveCallableToolNames(request))
	if decisionModel.startedCallCount != len(plan.requests)+1 {
		t.Fatalf("expected %d selection requests plus the routing call, got %d", len(plan.requests), decisionModel.startedCallCount)
	}
	if decisionModel.peakInFlightCount > maxConcurrentDecisionRequestCount {
		t.Fatalf("expected at most %d calls in flight, got %d", maxConcurrentDecisionRequestCount, decisionModel.peakInFlightCount)
	}
	for _, decisionRequest := range plan.requests {
		if byteCount := decisionRequestByteCount(decisionRequest); byteCount > decisionRequestByteBudget {
			t.Fatalf("expected every batch to fit the budget, got %d bytes", byteCount)
		}
	}
	if strings.Join(decisions.Messages[0].TurnFields.InitialToolNames, ",") != "provider_0_tool_0,provider_29_tool_49" {
		t.Fatalf("expected the needed tools from different batches, got %v", decisions.Messages[0].TurnFields.InitialToolNames)
	}
	toolSelection := recordedToolSelection(callLedger)
	if toolSelection == nil {
		t.Fatal("expected the selection to be recorded")
	}
	if toolSelection.CandidateCount != len(syntheticTools) {
		t.Fatalf("expected every candidate to be counted, got %d", toolSelection.CandidateCount)
	}
	if len(toolSelection.BatchByteCounts) != len(plan.requests) {
		t.Fatalf("expected one recorded byte count per batch, got %d", len(toolSelection.BatchByteCounts))
	}
	recordDocument, errorValue := json.Marshal(toolSelection)
	if errorValue != nil {
		t.Fatalf("expected the record to serialize: %v", errorValue)
	}
	if len(recordDocument) > recordedToolSelectionByteBudget {
		t.Fatalf("expected the record to stay small at %d candidates, got %d bytes", toolSelection.CandidateCount, len(recordDocument))
	}
}

const recordedToolSelectionByteBudget = 4096
