package routedharness

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type Harness struct {
	Inner  agentcontract.Harness
	router intake.TurnRouter
}

func New(inner agentcontract.Harness, languageModel model.LanguageModelProvider, decisionModel model.DecisionModel) Harness {
	router := intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(decisionModel, nil), agentcontract.IntakeOptions{IsEnabled: true})
	return Harness{Inner: inner, router: router}
}

func (routed Harness) RunTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if turnRequest.PrecomputedTurnDecision == nil {
		decision, errorValue := routed.router.Plan(ctx, turnRequest.RoutingRequest())
		if errorValue != nil {
			return agentcontract.AgentTurnResult{}, errorValue
		}
		turnRequest.PrecomputedTurnDecision = &decision
	}
	return routed.Inner.RunTurn(ctx, turnRequest)
}
