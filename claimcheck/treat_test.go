package claimcheck

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type scriptedWriter struct {
	prompts []string
	rewrite func(prompt string) string
}

func (writer *scriptedWriter) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (writer *scriptedWriter) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	prompt := request.Messages[0].Content
	writer.prompts = append(writer.prompts, prompt)
	return model.StructuredResponse{Content: writer.rewrite(prompt), Usage: model.Usage{PromptTokens: 7}}, nil
}

func treatedClaims() []Claim {
	return claimsOf("an invented fact", "a wrong date", "supercharge your day", "Thank you")
}

func TestTreatBlanksWhatCannotBeRewrittenAndRewritesSlop(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: func(text string) (string, map[string]float64) {
		if text == "Your day, planned" {
			return KindSource, map[string]float64{KindSource: 0.9}
		}
		return flaggingInventions(text)
	}}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"text":"Your day, planned"}` }}
	sources := Sources{Request: []string{"Write a notice."}}
	judgment, errorValue := Judge(context.Background(), decisionModel, sources, treatedClaims())
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	outcome, errorValue := Treat(context.Background(), CompactProfile, decisionModel, writer, sources, judgment)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(outcome.Blank) != 2 || len(outcome.Replaced) != 1 || len(outcome.Kept) != 0 {
		t.Fatalf("expected two blanked and the slop replaced, got %+v", outcome)
	}
	if outcome.Replaced[0].Path != "paragraphs[2]" || outcome.Replaced[0].Text != "Your day, planned" {
		t.Fatalf("expected the slop unit replaced by its rewrite, got %+v", outcome.Replaced)
	}
	if len(writer.prompts) != 1 || !strings.Contains(writer.prompts[0], "supercharge your day") || strings.Contains(writer.prompts[0], "a wrong date") {
		t.Fatalf("expected one rewrite that sees only the flagged unit, got %q", writer.prompts)
	}
}

func TestTreatKeepsSlopThatIsStillSlopAfterOneRewrite(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"text":"supercharge it differently"}` }}
	sources := Sources{Request: []string{"Write a notice."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("supercharge your day"))
	outcome, errorValue := Treat(context.Background(), CompactProfile, decisionModel, writer, sources, judgment)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(outcome.Kept) != 1 || len(outcome.Blank) != 0 || len(outcome.Replaced) != 0 || len(writer.prompts) != 1 {
		t.Fatalf("expected the original kept after one rewrite, got %+v after %d rewrites", outcome, len(writer.prompts))
	}
}

func TestTreatNeverBlanksSlopEvenWhenItsRewriteBecameAnotherDefect(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"text":"an invented guarantee"}` }}
	sources := Sources{Request: []string{"Write a notice."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("supercharge your day"))
	outcome, _ := Treat(context.Background(), CompactProfile, decisionModel, writer, sources, judgment)
	if len(outcome.Kept) != 1 || len(outcome.Blank) != 0 || len(outcome.Replaced) != 0 {
		t.Fatalf("expected slop never to be blanked even when its rewrite became another defect, got %+v", outcome)
	}
}

func TestTreatAsksNothingWhenNothingIsFlagged(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"text":"unused"}` }}
	sources := Sources{Request: []string{"Write a notice."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("Thank you"))
	outcome, _ := Treat(context.Background(), CompactProfile, decisionModel, writer, sources, judgment)
	if len(decisionModel.requests) != 1 || len(writer.prompts) != 0 || len(outcome.Blank)+len(outcome.Replaced)+len(outcome.Kept) != 0 {
		t.Fatalf("expected no further calls, got %d decisions, %d rewrites, %+v", len(decisionModel.requests), len(writer.prompts), outcome)
	}
}

func TestTreatKeepsSlopWhoseRewriteIsEmptyBecauseItHoldsNoFact(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: flaggingInventions}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"text":""}` }}
	sources := Sources{Request: []string{"Write a notice."}}
	judgment, _ := Judge(context.Background(), decisionModel, sources, claimsOf("supercharge your day"))
	outcome, _ := Treat(context.Background(), CompactProfile, decisionModel, writer, sources, judgment)
	if len(outcome.Kept) != 1 || len(outcome.Replaced) != 0 || len(outcome.Blank) != 0 {
		t.Fatalf("expected the original kept, got %+v", outcome)
	}
}

func TestTreatBlanksAnOverstatedUnitWhoseRewriteIsStillFlagged(t *testing.T) {
	decisionModel := &scriptedDecisionModel{kindOf: func(text string) (string, map[string]float64) {
		return KindOverstated, map[string]float64{KindOverstated: 0.9}
	}}
	writer := &scriptedWriter{rewrite: func(string) string { return `{"text":"still too strong"}` }}
	sources := Sources{Request: []string{"Write a report."}}
	judgment, _ := JudgeWith(context.Background(), FineProfile, decisionModel, sources, claimsOf("profit is confirmed"))
	outcome, _ := Treat(context.Background(), FineProfile, decisionModel, writer, sources, judgment)
	if len(outcome.Blank) != 1 || len(outcome.Kept) != 0 {
		t.Fatalf("expected an overstated unit that is still flagged to be blanked, got %+v", outcome)
	}
}
