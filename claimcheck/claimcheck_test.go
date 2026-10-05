package claimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type scriptedDecisionModel struct {
	requests []model.DecisionRequest
	kindOf   func(text string) (string, float64)
}

func (decisionModel *scriptedDecisionModel) Decide(_ context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	decisionModel.requests = append(decisionModel.requests, request)
	claims := request.State.(map[string]any)["claims"].(map[string]shownClaim)
	answers := map[string]model.DecisionAnswer{}
	for key := range request.Questions {
		kind, probability := decisionModel.kindOf(claims[key].Text)
		answers[key] = model.DecisionAnswer{Type: model.DecisionQuestionTypeChoice, Choice: kind, Probabilities: map[string]float64{KindClaim: probability}}
	}
	return model.DecisionResponse{Answers: answers, Usage: model.Usage{PromptTokens: 100, TotalTokens: 110}}, nil
}

func claimsOf(texts ...string) []Claim {
	claims := []Claim{}
	for index, text := range texts {
		claims = append(claims, Claim{Path: fmt.Sprintf("paragraphs[%d]", index), Text: text})
	}
	return claims
}

func flaggingInventions(text string) (string, float64) {
	if strings.Contains(text, "invented") {
		return KindClaim, 0.9
	}
	return KindSource, 0.05
}

func TestAClaimCopiedFromTheSourcesIsNotAsked(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	sources := Sources{
		Request:      []string{"Write a notice that the office  moves on 10/20."},
		Attachments:  []Attachment{{Name: "memo.txt", Text: "New address: 12 Sample Street"}},
		RuntimeFacts: json.RawMessage(`{"company":{"name":"Sample Co"}}`),
	}
	judgment, errorValue := Judge(context.Background(), decisionModel, sources, claimsOf("the office moves on 10/20.", "12 Sample Street", "Sample Co", "an invented promise"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisionModel.requests) != 1 || len(decisionModel.requests[0].Questions) != 1 {
		t.Fatalf("expected one call asking only the uncopied claim, got %+v", decisionModel.requests)
	}
	copied := 0
	for _, verdict := range judgment.Verdicts {
		if verdict.IsCopied {
			copied++
		}
	}
	if copied != 3 {
		t.Fatalf("expected three copied claims, got %d", copied)
	}
}

func TestOnlyAClaimAtOrAboveTheThresholdIsUnsupported(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	judgment, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf("Thank you for your support", "an invented commitment"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	unsupported := judgment.Unsupported()
	if len(unsupported) != 1 || unsupported[0].Path != "paragraphs[1]" || unsupported[0].Kind != KindClaim {
		t.Fatalf("expected only the invented commitment, got %+v", unsupported)
	}
	if judgment.Usage.TotalTokens != 110 {
		t.Fatalf("expected the call's usage, got %+v", judgment.Usage)
	}
}

func TestEveryQuestionAsksTheSameKindsAndTheStateHoldsTheirMeaning(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	_, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf("first", "second"))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	request := decisionModel.requests[0]
	if request.State.(map[string]any)["kinds"] == nil {
		t.Fatal("expected the kinds to be defined once in the state")
	}
	for key, question := range request.Questions {
		if question.Type != model.DecisionQuestionTypeChoice || len(question.Criteria.(map[string]string)) != len(kinds) {
			t.Fatalf("%s: expected a choice among every kind, got %+v", key, question)
		}
		if !strings.Contains(question.Instructions, "claims."+key) {
			t.Fatalf("%s: expected the question to name its own claim, got %q", key, question.Instructions)
		}
	}
}

func TestManyClaimsAreSplitIntoBoundedCalls(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	texts := []string{}
	for index := 0; index < claimsPerCall+1; index++ {
		texts = append(texts, fmt.Sprintf("value %d", index))
	}
	judgment, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a report."}}, claimsOf(texts...))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(decisionModel.requests) != 2 || len(judgment.Verdicts) != claimsPerCall+1 {
		t.Fatalf("expected two calls covering every claim, got %d calls and %d verdicts", len(decisionModel.requests), len(judgment.Verdicts))
	}
}

func TestAnAttachmentIsShownUpToItsBound(t *testing.T) {
	long := strings.Repeat("가", attachmentMaxRunes+10)
	bounded := boundedAttachments([]Attachment{{Name: "long.txt", Text: long}, {Name: "empty.txt", Text: "  "}})
	if len(bounded) != 1 || len([]rune(bounded[0].Text)) != attachmentMaxRunes {
		t.Fatalf("expected one attachment cut to %d runes, got %+v", attachmentMaxRunes, len(bounded))
	}
}

type silentDecisionModel struct{}

func (silentDecisionModel) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{Answers: map[string]model.DecisionAnswer{}}, nil
}

func TestAMissingAnswerIsAnError(t *testing.T) {
	_, errorValue := Judge(context.Background(), silentDecisionModel{}, Sources{Request: []string{"Write a notice."}}, claimsOf("a value"))
	if errorValue == nil {
		t.Fatal("expected a missing answer to fail the check")
	}
}

func TestWithdrawnValuesAreShownToTheJudgeAsRemovedAndOnlyWhenThereAreSome(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	if _, errorValue := Judge(context.Background(), decisionModel, Sources{Request: []string{"Write a notice."}}, claimsOf("first")); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, hasRemoved := decisionModel.requests[0].State.(map[string]any)["removed"]; hasRemoved {
		t.Fatal("a judgment with nothing removed showed a removed list")
	}
	sources := Sources{Request: []string{"Write a notice."}, Removed: []Claim{{Path: "slides[0].units[1]", At: "slide 1", Text: "Sign the lease"}}}
	if _, errorValue := Judge(context.Background(), decisionModel, sources, claimsOf("Two decisions today")); errorValue != nil {
		t.Fatal(errorValue)
	}
	removed, isShown := decisionModel.requests[1].State.(map[string]any)["removed"].([]shownClaim)
	if !isShown || len(removed) != 1 || removed[0].Text != "Sign the lease" || removed[0].At != "slide 1" {
		t.Fatalf("removed shown as %+v", decisionModel.requests[1].State.(map[string]any)["removed"])
	}
}

func TestAStatementThatCountsOrNamesRemovedValuesIsDefinedAsAClaim(t *testing.T) {
	if !strings.Contains(kinds[KindClaim], "removed") {
		t.Fatalf("the claim kind does not mention removed values: %q", kinds[KindClaim])
	}
	if !strings.Contains(about, "removed") {
		t.Fatalf("the preface does not explain removed: %q", about)
	}
}
