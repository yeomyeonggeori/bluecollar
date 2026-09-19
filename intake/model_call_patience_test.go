package intake

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type stallingThenAnsweringTurnWordsModel struct {
	stalledCalls int
	requests     []model.StructuredResponseRequest
}

func (languageModel *stallingThenAnsweringTurnWordsModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *stallingThenAnsweringTurnWordsModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.requests = append(languageModel.requests, request)
	if len(languageModel.requests) <= languageModel.stalledCalls {
		<-ctx.Done()
		return model.StructuredResponse{}, ctx.Err()
	}
	return model.StructuredResponse{
		ModelName: "a-model",
		Content:   `{"reason":"the deadline is missing","userFacingReply":"","clarificationQuestion":"언제까지 필요하세요?","clarificationOptions":[],"busyInstruction":"","expectedResults":[]}`,
	}, nil
}

func patientTurnRouter(languageModel model.LanguageModelProvider) TurnRouter {
	decisionPlanner := NewDecisionPlanner(intaketest.NewDecisionModel(clarifyOutcome()), nil, func() float64 { return 1 })
	return NewTurnRouter(languageModel, decisionPlanner, enabledIntakeOptions())
}

func TestAStalledTurnWordsCallIsCutAtTheMeasuredPatienceAndAskedAgain(t *testing.T) {
	languageModel := &stallingThenAnsweringTurnWordsModel{stalledCalls: 1}
	turnRouter := patientTurnRouter(languageModel)
	turnRouter.callCost.record("a-model", time.Millisecond)

	decision, errorValue := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{Prompt: "보고서 하나 만들어줘", ResponseLanguage: "ko"})

	if errorValue != nil || decision.ClarificationQuestion != "언제까지 필요하세요?" {
		t.Fatalf("the second ask should have written the turn: %v %+v", errorValue, decision)
	}
	if len(languageModel.requests) != 2 {
		t.Fatalf("expected one cut and one answer, got %d asks", len(languageModel.requests))
	}
}

func TestAnUnmeasuredTurnWordsCallIsNotCut(t *testing.T) {
	languageModel := &stallingThenAnsweringTurnWordsModel{stalledCalls: 1}
	turnRouter := patientTurnRouter(languageModel)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, errorValue := turnRouter.Plan(ctx, agentcontract.AgentRequest{Prompt: "보고서 하나 만들어줘", ResponseLanguage: "ko"})

	if errorValue == nil || len(languageModel.requests) != 1 {
		t.Fatalf("without a measured cost the only clock is the caller's: %v after %d asks", errorValue, len(languageModel.requests))
	}
}

func TestACallerCancellationOfTheTurnWordsCallIsNotACut(t *testing.T) {
	languageModel := &stallingThenAnsweringTurnWordsModel{stalledCalls: 2}
	turnRouter := patientTurnRouter(languageModel)
	turnRouter.callCost.record("a-model", time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, errorValue := turnRouter.Plan(ctx, agentcontract.AgentRequest{Prompt: "보고서 하나 만들어줘", ResponseLanguage: "ko"})

	if errorValue == nil || len(languageModel.requests) != 1 {
		t.Fatalf("the caller's own deadline ends the ask without a retry: %v after %d asks", errorValue, len(languageModel.requests))
	}
}

type stallingThenAnsweringDecisionModel struct {
	stalledCalls int
	callCount    int
	answering    model.DecisionModel
}

func (decisionModel *stallingThenAnsweringDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.callCount++
	if decisionModel.callCount <= decisionModel.stalledCalls {
		<-ctx.Done()
		return model.DecisionResponse{}, ctx.Err()
	}
	response, errorValue := decisionModel.answering.Decide(ctx, request)
	response.ModelName = "a-decision-model"
	return response, errorValue
}

func TestAStalledDecisionCallIsCutAtTheMeasuredPatienceAndAskedAgain(t *testing.T) {
	decisionModel := &stallingThenAnsweringDecisionModel{stalledCalls: 1, answering: intaketest.NewDecisionModel(clarifyOutcome())}
	decisionPlanner := NewDecisionPlanner(decisionModel, nil, func() float64 { return 1 })
	decisionPlanner.callCost.record("a-decision-model", time.Millisecond)
	callLedger := &agentcontract.IntakeCallLedger{}

	decisions, errorValue := decisionPlanner.Decide(context.Background(), agentcontract.IntakeDecisionRequest{
		Messages: []agentcontract.IntakeDecisionMessage{{MessageID: "message-1", Prompt: "보고서 하나 만들어줘"}},
	}, callLedger)

	if errorValue != nil || len(decisions.Messages) != 1 {
		t.Fatalf("the second ask should have decided: %v %+v", errorValue, decisions)
	}
	if decisionModel.callCount != 2 {
		t.Fatalf("expected one cut and one answer, got %d asks", decisionModel.callCount)
	}
	if len(callLedger.Records) == 0 || !callLedger.Records[0].UsedFallback {
		t.Fatalf("the cut should be on the intake call ledger: %+v", callLedger.Records)
	}
}
