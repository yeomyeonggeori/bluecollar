package intake

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type concurrencyCountingDecisionModel struct {
	outcome intaketest.Outcome

	mutex                sync.Mutex
	inFlightCount        int
	peakInFlightCount    int
	failSelectionBatches bool
	startedCallCount     int
	startedBatchCount    int
	cancelledBatchCount  int
}

func (decisionModel *concurrencyCountingDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.mutex.Lock()
	decisionModel.inFlightCount++
	decisionModel.startedCallCount++
	decisionModel.peakInFlightCount = max(decisionModel.peakInFlightCount, decisionModel.inFlightCount)
	isSelectionBatch := decisionModel.failSelectionBatches && asksAboutTools(request.Questions)
	if isSelectionBatch {
		decisionModel.startedBatchCount++
	}
	isFirstBatch := isSelectionBatch && decisionModel.startedBatchCount == 1
	decisionModel.mutex.Unlock()
	defer func() {
		decisionModel.mutex.Lock()
		decisionModel.inFlightCount--
		decisionModel.mutex.Unlock()
	}()
	if isFirstBatch {
		select {
		case <-ctx.Done():
			decisionModel.mutex.Lock()
			decisionModel.cancelledBatchCount++
			decisionModel.mutex.Unlock()
			return model.DecisionResponse{}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if isSelectionBatch {
		return model.DecisionResponse{}, context.DeadlineExceeded
	}
	return model.DecisionResponse{Answers: intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return decisionModel.outcome })}, nil
}

func asksAboutTools(questions map[string]model.DecisionQuestion) bool {
	for questionName := range questions {
		if _, isToolQuestion := toolNameOfQuestion(questionName); isToolQuestion {
			return true
		}
	}
	return false
}

func TestManyBatchesNeverRunMoreDecisionCallsAtOnceThanTheCap(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.TurnDecision.InitialToolNames = nil
	decisionModel := &concurrencyCountingDecisionModel{outcome: outcome}
	requests := make([]model.DecisionRequest, 40)

	NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }).decideEveryRequest(context.Background(), requests)

	if decisionModel.peakInFlightCount > maxConcurrentDecisionRequestCount {
		t.Fatalf("expected at most %d calls in flight, got %d", maxConcurrentDecisionRequestCount, decisionModel.peakInFlightCount)
	}
	if decisionModel.startedCallCount != len(requests) {
		t.Fatalf("expected every request to be sent, got %d of %d", decisionModel.startedCallCount, len(requests))
	}
}

func TestOneFailedBatchCancelsTheRestAndSelectsNothing(t *testing.T) {
	request := burstDecisionRequest(burstMessageCountThatOverflowsTheBudget, measurementToolNames())
	outcome := startTaskOutcome()
	outcome.ToolProbabilities = map[string]float64{"web_search": 0.88}
	decisionModel := &concurrencyCountingDecisionModel{outcome: outcome, failSelectionBatches: true}
	callLedger := &agentcontract.IntakeCallLedger{}

	startedAt := time.Now()
	decisions, errorValue := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 }).Decide(context.Background(), request, callLedger)

	if errorValue != nil {
		t.Fatalf("expected a failed selection to leave the task startable: %v", errorValue)
	}
	if time.Since(startedAt) > time.Second {
		t.Fatalf("expected the failure to cancel the batches still running, got %s", time.Since(startedAt))
	}
	for _, decision := range decisions.Messages {
		if len(decision.TurnFields.InitialToolNames) != 0 {
			t.Fatalf("expected no partial selection, got %v", decision.TurnFields.InitialToolNames)
		}
	}
	if recordedToolSelection(callLedger) != nil {
		t.Fatalf("expected no selection record for a failed selection, got %+v", recordedToolSelection(callLedger))
	}
	if decisionModel.cancelledBatchCount != 1 {
		t.Fatalf("expected the running batch to be cancelled by the failure, got %d", decisionModel.cancelledBatchCount)
	}
}
