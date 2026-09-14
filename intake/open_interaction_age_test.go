package intake

import (
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestARouterSeesHowStaleAPendingQuestionIs(t *testing.T) {
	now := time.Date(2026, 9, 14, 15, 8, 0, 0, time.UTC)
	request := agentcontract.AgentRequest{
		EnvironmentNow: now,
		PendingConfirmation: agentcontract.PendingConfirmationContext{
			TaskRunID:      "run-1",
			Prompt:         "15일 일정 삭제",
			Question:       "삭제할까요?",
			AskedAt:        now.Add(-2*time.Hour - 28*time.Minute),
			ExchangesSince: 3,
		},
	}

	description := turnRoutingContextDescription(request)

	for _, expected := range []string{"Asked 2h28m0s ago", "3 exchange(s) have happened since", "no longer names it"} {
		if !strings.Contains(description, expected) {
			t.Fatalf("the router must see the age and the exchanges since, got %q", description)
		}
	}
	fresh := agentcontract.AgentRequest{EnvironmentNow: now, PendingInput: agentcontract.PendingInputContext{TaskRunID: "run-2", Question: "어느 채널?", AskedAt: now.Add(-time.Minute)}}
	if !strings.Contains(turnRoutingContextDescription(fresh), "nothing has been exchanged since") {
		t.Fatalf("a fresh question is answered by a bare answer, got %q", turnRoutingContextDescription(fresh))
	}
}
