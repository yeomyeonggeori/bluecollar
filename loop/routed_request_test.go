package loop

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const routedRequestRoutingTimeout = 30 * time.Second

func scriptedRouterDecisionModel(languageModel model.LanguageModelProvider) *intaketest.LanguageModelDecisionModel {
	return &intaketest.LanguageModelDecisionModel{
		LanguageModel: languageModel,
		Addressing:    agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true},
	}
}

func routedRequest(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) AgentRequest {
	t.Helper()
	if request.PrecomputedTurnDecision != nil {
		return request
	}
	boundedRoutingContext, cancelRouting := context.WithTimeout(responseContext, routedRequestRoutingTimeout)
	defer cancelRouting()
	languageModel := agentKernel.turnRouterLanguageModel()
	decisionPlanner := intake.NewDecisionPlanner(scriptedRouterDecisionModel(languageModel), nil, func() float64 { return 1 })
	turnDecision, errorValue := intake.NewTurnRouter(languageModel, decisionPlanner, agentKernel.intakeOptions).Plan(boundedRoutingContext, request)
	if errorValue != nil {
		return request
	}
	request.PrecomputedTurnDecision = &turnDecision
	return request
}
