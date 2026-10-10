package intake

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func TestRouteIsReadByTheHeaviestGroupOfRoutes(t *testing.T) {
	testCases := []struct {
		name          string
		probabilities map[string]float64
		wantRoute     agentcontract.TurnRoute
	}{
		{
			"work split three ways outweighs a single give_up",
			map[string]float64{"give_up": 0.24, "start_task": 0.23, "continue_task": 0.13, "revise_task": 0.11, "answer_question": 0.19, "clarify": 0.1},
			agentcontract.TurnRouteStartTask,
		},
		{
			"give_up stands when it outweighs all work together",
			map[string]float64{"give_up": 0.55, "start_task": 0.2, "continue_task": 0.1, "answer_question": 0.15},
			agentcontract.TurnRouteGiveUp,
		},
		{
			"words split two ways outweigh a single clarify",
			map[string]float64{"clarify": 0.35, "answer_question": 0.3, "answer_meta": 0.2, "start_task": 0.15},
			agentcontract.TurnRouteAnswerQuestion,
		},
		{
			"the likeliest route inside the winning group is the one taken",
			map[string]float64{"start_task": 0.2, "continue_task": 0.35, "revise_task": 0.1, "give_up": 0.3},
			agentcontract.TurnRouteContinueTask,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			answer := model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: "give_up", Probabilities: testCase.probabilities}

			if route := routeByGroupedBelief(answer); route != testCase.wantRoute {
				t.Fatalf("expected %q, got %q", testCase.wantRoute, route)
			}
		})
	}
}

func TestRouteFallsBackToTheChoiceWithoutADistribution(t *testing.T) {
	answer := model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: " clarify "}

	if route := routeByGroupedBelief(answer); route != agentcontract.TurnRouteClarify {
		t.Fatalf("expected the bare choice, got %q", route)
	}
}

func TestPlannerStartsWorkThatGiveUpOnlyLeadsAsASingleOption(t *testing.T) {
	outcome := startTaskOutcome()
	outcome.TurnDecision.Route = agentcontract.TurnRouteGiveUp
	outcome.TurnDecision.HasIndependentWork = true
	outcome.RouteProbabilities = map[string]float64{"give_up": 0.24, "start_task": 0.23, "continue_task": 0.13, "revise_task": 0.11, "answer_question": 0.19, "clarify": 0.1}
	decision := decideOnce(t, NewDecisionPlanner(intaketest.NewDecisionModel(outcome), nil), addressedDecisionRequest("포틀랜드 기준 23시 퇴근 찍어줘"))

	if decision.TurnFields.Route != agentcontract.TurnRouteStartTask {
		t.Fatalf("expected start_task, got %q", decision.TurnFields.Route)
	}
	if decision.TurnFields.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("expected a bounded task, got %q", decision.TurnFields.Classification)
	}
	if decision.TurnFields.RawDecisionRoute != agentcontract.TurnRouteGiveUp {
		t.Fatalf("expected the ledger to keep the model's own pick, got %q", decision.TurnFields.RawDecisionRoute)
	}
}
