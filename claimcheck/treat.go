package claimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const TreatmentKeep Treatment = "keep"

const rewriteSchema = `{"type":"object","properties":{"text":{"type":"string","description":"the rewritten unit, nothing else"}},"required":["text"],"additionalProperties":false}`

type rewriteAnswer struct {
	Text string `json:"text"`
}

func askStructured(ctx context.Context, writer model.LanguageModelProvider, schema string, prompt string, answer any) (model.Usage, error) {
	response, errorValue := writer.GenerateStructuredResponse(ctx, model.StructuredResponseRequest{
		Messages:               []model.Message{{Role: "user", Content: prompt}},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_claim_check", Document: schema, IsStrictlyEnforced: true},
	})
	if errorValue != nil {
		return model.Usage{}, errorValue
	}
	if errorValue := json.Unmarshal([]byte(response.Content), answer); errorValue != nil {
		return model.Usage{}, fmt.Errorf("the answer %q does not fit the schema: %w", response.Content, errorValue)
	}
	return response.Usage, nil
}

type Outcome struct {
	Blank    []Verdict `json:"blank,omitempty"`
	Replaced []Claim   `json:"replaced,omitempty"`
	Removed  []Verdict `json:"removed,omitempty"`
	Kept     []Verdict `json:"kept,omitempty"`
	Usage    model.Usage
}

func Treat(ctx context.Context, profile Profile, decisionModel model.DecisionModel, writer model.LanguageModelProvider, sources Sources, judgment Judgment) (Outcome, error) {
	outcome := Outcome{Blank: judgment.Treated(TreatmentBlank)}
	toRewrite := judgment.Treated(TreatmentRewrite)
	if len(toRewrite) == 0 {
		return outcome, nil
	}
	rewrites, errorValue := rewrittenClaims(ctx, profile, writer, sources, toRewrite, &outcome.Usage)
	if errorValue != nil {
		return Outcome{}, errorValue
	}
	rechecked, errorValue := JudgeWith(ctx, profile, decisionModel, sources, rewrites)
	if errorValue != nil {
		return Outcome{}, errorValue
	}
	outcome.Usage = addedUsage(outcome.Usage, rechecked.Usage)
	for index, verdict := range rechecked.Verdicts {
		outcome.place(profile, toRewrite[index], verdict)
	}
	return outcome, nil
}

func (outcome *Outcome) place(profile Profile, original Verdict, rewritten Verdict) {
	switch {
	case strings.TrimSpace(rewritten.Text) == "" && original.IsFree:
		outcome.Removed = append(outcome.Removed, original)
	case strings.TrimSpace(rewritten.Text) == "":
		outcome.fallBack(profile, original)
	case rewritten.Defect == "":
		outcome.Replaced = append(outcome.Replaced, rewritten.Claim)
	default:
		outcome.fallBack(profile, original)
	}
}

func (outcome *Outcome) fallBack(profile Profile, original Verdict) {
	if profile.Fallback[original.Defect] == TreatmentKeep {
		outcome.Kept = append(outcome.Kept, original)
		return
	}
	outcome.Blank = append(outcome.Blank, original)
}

func rewrittenClaims(ctx context.Context, profile Profile, writer model.LanguageModelProvider, sources Sources, verdicts []Verdict, spent *model.Usage) ([]Claim, error) {
	rewrites := make([]Claim, 0, len(verdicts))
	for _, verdict := range verdicts {
		var answer rewriteAnswer
		usage, errorValue := askStructured(ctx, writer, rewriteSchema, rewritePrompt(profile, sources, verdict), &answer)
		if errorValue != nil {
			return nil, fmt.Errorf("rewrite %s: %w", verdict.Path, errorValue)
		}
		*spent = addedUsage(*spent, usage)
		rewrites = append(rewrites, Claim{Path: verdict.Path, At: verdict.At, Text: strings.TrimSpace(answer.Text)})
	}
	return rewrites, nil
}

func rewritePrompt(profile Profile, sources Sources, verdict Verdict) string {
	state := batchState(profile, sources, nil)
	delete(state, "claims")
	delete(state, "kinds")
	encoded, _ := json.Marshal(state)
	return fmt.Sprintf("You rewrite one unit of a business document. %s\nSources: %s\nUnit: %s\nAnswer with the rewritten unit as the text field. Keep every fact, number and name the unit already has, and add none.",
		profile.Rewrites[verdict.Defect], encoded, verdict.Text)
}
