//go:build llmeval

package intake

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/evaltest"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
)

func TestClarificationRecoveryWithLiveModel(t *testing.T) {
	apiKey := evaltest.RequireInput(t, "OPENROUTER_API_KEY", "run under monkeys run @test")
	provider, errorValue := openaicompatible.Endpoint{URL: routerEvaluationEndpoint, ModelName: routerEvaluationModel, APIKey: apiKey, ProviderSort: "throughput", ReasoningEffort: "low"}.Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	provider.UseHTTPClient(&http.Client{Transport: &armTransport{exchanges: &exchangeJournal{path: os.Getenv(routerEvaluationExchangesName)}}, Timeout: 2 * time.Minute})
	planner := NewDecisionPlanner(intaketest.NewDecisionModel(clarifyOutcome()), nil)
	router := NewTurnRouter(provider, planner, enabledIntakeOptions())

	for _, fixture := range clarificationRecoveryFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			decision, errorValue := router.Plan(context.Background(), fixture.request)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			if decision.Route != fixture.route || decision.RoutingFallbackReason != "" {
				t.Fatalf("expected a reviewed %s route, got %+v", fixture.route, decision)
			}
			if fixture.route == agentcontract.TurnRouteClarify && decision.ClarificationQuestion == "" {
				t.Fatal("essential missing information must produce a question")
			}
			if fixture.route == agentcontract.TurnRouteStartTask && decision.ClarificationQuestion != "" {
				t.Fatalf("answered choices must start work without another question: %+v", decision)
			}
			t.Logf("route=%s question=%q results=%d", decision.Route, decision.ClarificationQuestion, len(decision.ExpectedResults))
		})
	}
}

type clarificationRecoveryFixture struct {
	name    string
	request agentcontract.AgentRequest
	route   agentcontract.TurnRoute
}

func clarificationRecoveryFixtures() []clarificationRecoveryFixture {
	return []clarificationRecoveryFixture{
		{
			name: "both_requested_formats",
			request: agentcontract.AgentRequest{
				Prompt: "둘 다", ResponseLanguage: "ko", ConversationType: "direct",
				VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
					{Speaker: "이샘플", Text: "월별 매출표를 PDF와 엑셀로 둘 다 만들어줘. 수치는 1월 100, 2월 150, 3월 200이야."},
					{Speaker: "Assistant", Text: "PDF와 엑셀 중 어느 형식으로 만들까요?"},
				}},
				ActiveGoal: agentcontract.ActiveGoal{TaskRunID: "sample-goal", OriginalInstruction: "월별 매출표를 PDF와 엑셀로 둘 다 만들어줘."},
			},
			route: agentcontract.TurnRouteStartTask,
		},
		{
			name:    "essential_recipient_is_missing",
			request: agentcontract.AgentRequest{Prompt: "내가 생각하는 거래처에 이 견적서를 보내줘. 거래처는 아직 알려주지 않았어.", ResponseLanguage: "ko", ConversationType: "direct"},
			route:   agentcontract.TurnRouteClarify,
		},
	}
}
