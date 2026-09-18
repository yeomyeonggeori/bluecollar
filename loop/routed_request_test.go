package loop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const routedRequestRoutingTimeout = 30 * time.Second

// scriptedRouterDecisionModel reads the turn a kernel test scripts on its intake
// chat model and answers the decision questions from it, which is what the host
// now does with a real decision model.
type scriptedRouterDecisionModel struct {
	languageModel model.LanguageModelProvider
}

func (decisionModel scriptedRouterDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	response, errorValue := decisionModel.languageModel.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: agentcontract.TurnRouterSchemaName},
	})
	if errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	var turnDecision TurnDecision
	if errorValue := json.Unmarshal([]byte(response.Content), &turnDecision); errorValue != nil {
		return model.DecisionResponse{}, errorValue
	}
	outcome := intaketest.Outcome{
		Addressing:   agentcontract.AddressingDecision{Target: agentcontract.AddressingTargetBot, ShouldRespond: true},
		TurnDecision: turnDecision,
	}
	return model.DecisionResponse{Answers: intaketest.Answers(request.Questions, func(string) intaketest.Outcome { return outcome })}, nil
}

func routedRequest(t *testing.T, responseContext context.Context, agentKernel *AgentKernel, request AgentRequest) AgentRequest {
	t.Helper()
	if request.PrecomputedTurnDecision != nil {
		return request
	}
	boundedRoutingContext, cancelRouting := context.WithTimeout(responseContext, routedRequestRoutingTimeout)
	defer cancelRouting()
	languageModel := agentKernel.turnRouterLanguageModel()
	decisionPlanner := intake.NewDecisionPlanner(scriptedRouterDecisionModel{languageModel: languageModel}, nil, func() float64 { return 1 })
	turnDecision, errorValue := intake.NewTurnRouter(languageModel, decisionPlanner, agentKernel.intakeOptions).Plan(boundedRoutingContext, request)
	if errorValue != nil {
		return request
	}
	request.PrecomputedTurnDecision = &turnDecision
	return request
}
