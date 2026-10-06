package routedharness

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type harness struct {
	inner  agentcontract.Harness
	router intake.TurnRouter
}

func New(inner agentcontract.Harness, languageModel model.LanguageModelProvider, decisionModel model.DecisionModel) agentcontract.Harness {
	router := intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(decisionModel, nil), agentcontract.IntakeOptions{IsEnabled: true})
	return harness{inner: inner, router: router}
}

func (routed harness) RunTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if turnRequest.PrecomputedTurnDecision == nil {
		decision, errorValue := routed.router.Plan(ctx, turnRequest.RoutingRequest())
		if errorValue != nil {
			return agentcontract.AgentTurnResult{}, errorValue
		}
		turnRequest.PrecomputedTurnDecision = &decision
	}
	return routed.inner.RunTurn(ctx, turnRequest)
}
