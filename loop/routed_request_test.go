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
	return &intaketest.LanguageModelDecisionModel{LanguageModel: languageModel}
}

func runRoutedRequest(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) (AgentTurnResult, error) {
	t.Helper()
	return agentKernel.RunAgentRequest(responseContext, routingFor(t, responseContext, agentKernel, request), request)
}

func routingFor(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) agentcontract.Routing {
	t.Helper()
	boundedRoutingContext, cancelRouting := context.WithTimeout(responseContext, routedRequestRoutingTimeout)
	defer cancelRouting()
	languageModel := agentKernel.turnRouterLanguageModel()
	decisionPlanner := intake.NewDecisionPlanner(scriptedRouterDecisionModel(languageModel), nil)
	turnDecision, errorValue := intake.NewTurnRouter(languageModel, decisionPlanner, agentKernel.intakeOptions).Plan(boundedRoutingContext, request)
	if errorValue != nil {
		return agentcontract.Routing{}
	}
	return agentcontract.Routing{Decision: &turnDecision}
}
