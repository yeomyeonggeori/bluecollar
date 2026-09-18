package intaketest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

// LanguageModelDecisionModel answers the decision questions from the turn a
// chat model is scripted to return for the router schema. A harness written
// before one call decided everything scripts a turn rather than an answer set,
// and this keeps that script meaning what it meant.
type LanguageModelDecisionModel struct {
	LanguageModel model.LanguageModelProvider
	Addressing    agentcontract.AddressingDecision
	ModelName     string
}

var ErrLanguageModelUnavailable = errors.New("decision language model unavailable")

func (decisionModel LanguageModelDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	if decisionModel.LanguageModel == nil {
		return model.DecisionResponse{}, ErrLanguageModelUnavailable
	}
	response, errorValue := decisionModel.LanguageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: agentcontract.TurnRouterSchemaName},
	})
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	var turnDecision agentcontract.TurnDecision
	if errorValue := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &turnDecision); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	outcome := Outcome{
		Addressing:        decisionModel.Addressing,
		TurnDecision:      turnDecision,
		PendingChoiceKeys: PendingChoiceKeys(request.State),
	}
	return model.DecisionResponse{
		Answers:   Answers(request.Questions, func(string) Outcome { return outcome }),
		ModelName: decisionModel.ModelName,
	}, nil
}
