//go:build llmeval

package loop

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const holdsLiveAttempts = 3

type holdsVerdict struct {
	carriedOut   float64
	promptTokens int64
}

type tokenCountingDecisionModel struct {
	inner        model.DecisionModel
	promptTokens int64
}

func (counting *tokenCountingDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	response, errorValue := counting.inner.Decide(ctx, request)
	counting.promptTokens = response.Usage.PromptTokens
	return response, errorValue
}

func TestLiveChangeCheckJudgesDeliveredOfficeFilesByWhatTheyHold(t *testing.T) {
	endpoint := liveDecisionEndpoint(t)
	cases := deliveredOfficeFileCases(t)
	wrongBefore, wrongAfter := 0, 0
	tokensBefore, tokensAfter := int64(0), int64(0)
	for _, liveCase := range cases {
		request, expected, observations := deliveredOfficeFileTurn(liveCase)
		for attempt := 0; attempt < holdsLiveAttempts; attempt++ {
			before := judgeChange(t, endpoint, request, expected, withoutHolds(observations))
			after := judgeChange(t, endpoint, request, expected, observations)
			wrongBefore += wrongVerdict(before, liveCase.IsCarriedOut)
			wrongAfter += wrongVerdict(after, liveCase.IsCarriedOut)
			tokensBefore += before.promptTokens
			tokensAfter += after.promptTokens
			t.Logf("%-55s carriedOut=%v before=%.2f (%d tokens) after=%.2f (%d tokens)", liveCase.Name, liveCase.IsCarriedOut, before.carriedOut, before.promptTokens, after.carriedOut, after.promptTokens)
		}
	}
	verdicts := len(cases) * holdsLiveAttempts
	t.Logf("wrong verdicts: before %d/%d, after %d/%d; prompt tokens per check: before %d, after %d", wrongBefore, verdicts, wrongAfter, verdicts, tokensBefore/int64(verdicts), tokensAfter/int64(verdicts))
	if wrongAfter > wrongBefore {
		t.Fatalf("holds made the change check worse: %d wrong verdicts before, %d after", wrongBefore, wrongAfter)
	}
}

func TestLiveChangeCheckAcceptsW1RecordedWorkbookByWhatItHolds(t *testing.T) {
	endpoint := liveDecisionEndpoint(t)
	delivery := w1DeliveredWorkbook(t)
	request := AgentTurnRequest{Prompt: delivery.Prompt, ToolSet: commandFileToolSet(), EnvironmentNow: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	expected := []expectedChange{{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}}
	held := append([]turnObservation(nil), delivery.Observations...)
	held[len(held)-1].Attachments = []toolcontract.FileAttachment{held[len(held)-1].Attachments[0]}
	held[len(held)-1].Attachments[0].Holds = delivery.Holds
	for attempt := 0; attempt < holdsLiveAttempts; attempt++ {
		before := judgeChange(t, endpoint, request, expected, delivery.Observations)
		after := judgeChange(t, endpoint, request, expected, held)
		t.Logf("W1 run 1 recorded: before=%.2f (%d tokens) after=%.2f (%d tokens)", before.carriedOut, before.promptTokens, after.carriedOut, after.promptTokens)
		if after.carriedOut < changeCarriedOutThreshold {
			t.Errorf("expected the recorded workbook carried out once its holds are seen, got %.2f", after.carriedOut)
		}
	}
}

func liveDecisionEndpoint(t *testing.T) decisions.Endpoint {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Fatal("OPENROUTER_API_KEY is not set; this evaluation judges with the live decision model and has no offline answer")
	}
	return decisions.Endpoint{URL: decisions.DefaultEndpointURL, ModelName: firstNonEmptyString(os.Getenv("BLUECOLLAR_DECISION_MODEL"), "~typesafe/jev-latest"), APIKey: apiKey}
}

func withoutHolds(observations []turnObservation) []turnObservation {
	stripped := append([]turnObservation(nil), observations...)
	for index := range stripped {
		attachments := append([]toolcontract.FileAttachment(nil), stripped[index].Attachments...)
		for attachmentIndex := range attachments {
			attachments[attachmentIndex].Holds = nil
		}
		stripped[index].Attachments = attachments
	}
	return stripped
}

func judgeChange(t *testing.T, endpoint decisions.Endpoint, request AgentTurnRequest, expected []expectedChange, observations []turnObservation) holdsVerdict {
	counting := &tokenCountingDecisionModel{inner: endpoint.DecisionModel()}
	check, errorValue := checkExpectedChanges(context.Background(), counting, request, expected, observations)
	if errorValue != nil {
		t.Fatalf("the change check failed: %v", errorValue)
	}
	return holdsVerdict{carriedOut: check.CarriedOut[changeQuestionKey(0)], promptTokens: counting.promptTokens}
}

func wrongVerdict(verdict holdsVerdict, isCarriedOut bool) int {
	if (verdict.carriedOut >= changeCarriedOutThreshold) != isCarriedOut {
		return 1
	}
	return 0
}
