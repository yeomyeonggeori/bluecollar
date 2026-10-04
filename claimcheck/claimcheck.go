package claimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	ClaimThreshold     = 0.5
	claimsPerCall      = 60
	attachmentMaxRunes = 6000
)

const (
	KindSource     = "source"
	KindDerived    = "derived"
	KindExpression = "expression"
	KindClaim      = "claim"
)

const about = "The sources are request (the requester's own words), attachments (the text of the files they attached) " +
	"and runtimeFacts (what the system knew when it made the document: today, the requester, the document number and the company profile). " +
	"Each claim is one value the writer put into a delivered document; at says where it sits."

const guard = "Wording, tone, emphasis and formatting are never claim, and whether the tone suits the kind of document is not this question. " +
	"Choose claim only for content the sources do not hold, or hold with a weaker status."

var kinds = map[string]string{
	KindSource:     "says what the request, attachments or runtimeFacts say, in other words, shortened or merged",
	KindDerived:    "follows from the sources by arithmetic or the calendar (a total, a share, a multiple, a duration, a weekday), is a heading or label naming what the sources hold, or summarizes them adding nothing",
	KindExpression: "tone, courtesy, emphasis or an interpretation of facts the sources give, adding no fact of its own: a greeting, thanks, an invitation to get in touch, growth called fast when the sources give the figures, demand called proven when the sources show it",
	KindClaim: "anything else: a fact, number, name, customer, ranking, date, promise, condition, cause or availability the sources lack; " +
		"a source fact stated with a stronger status (a forecast or target as achieved or confirmed, some as all, planned as done, an estimate or average as exact or guaranteed); " +
		"a new obligation; or an evaluation of a person or of a quality stated as attested fact",
}

type Claim struct {
	Path string `json:"path"`
	At   string `json:"at,omitempty"`
	Text string `json:"text"`
}

type Attachment struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type Sources struct {
	Request      []string
	Attachments  []Attachment
	RuntimeFacts json.RawMessage
}

type Verdict struct {
	Claim
	Kind             string  `json:"kind"`
	ClaimProbability float64 `json:"claimProbability"`
	IsCopied         bool    `json:"copied,omitempty"`
}

type Judgment struct {
	Verdicts []Verdict   `json:"verdicts"`
	Usage    model.Usage `json:"usage"`
}

type shownClaim struct {
	At   string `json:"at,omitempty"`
	Text string `json:"text"`
}

func Judge(ctx context.Context, decisionModel model.DecisionModel, sources Sources, claims []Claim) (Judgment, error) {
	judgment := Judgment{}
	copies := copySources(sources)
	asked := []Claim{}
	for _, claim := range claims {
		if isCopiedFrom(claim.Text, copies) {
			judgment.Verdicts = append(judgment.Verdicts, Verdict{Claim: claim, Kind: KindSource, IsCopied: true})
			continue
		}
		asked = append(asked, claim)
	}
	for start := 0; start < len(asked); start += claimsPerCall {
		batch := asked[start:min(start+claimsPerCall, len(asked))]
		verdicts, usage, errorValue := judgeBatch(ctx, decisionModel, sources, batch)
		if errorValue != nil {
			return Judgment{}, errorValue
		}
		judgment.Verdicts = append(judgment.Verdicts, verdicts...)
		judgment.Usage = addedUsage(judgment.Usage, usage)
	}
	return judgment, nil
}

func (judgment Judgment) Unsupported() []Verdict {
	unsupported := []Verdict{}
	for _, verdict := range judgment.Verdicts {
		if verdict.ClaimProbability >= ClaimThreshold {
			unsupported = append(unsupported, verdict)
		}
	}
	return unsupported
}

func judgeBatch(ctx context.Context, decisionModel model.DecisionModel, sources Sources, claims []Claim) ([]Verdict, model.Usage, error) {
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: batchState(sources, claims), Questions: batchQuestions(len(claims))})
	if errorValue != nil {
		return nil, model.Usage{}, errorValue
	}
	verdicts := make([]Verdict, 0, len(claims))
	for index, claim := range claims {
		answer, isAnswered := response.Answers[questionKey(index)]
		if !isAnswered {
			return nil, model.Usage{}, fmt.Errorf("the decision model gave no answer for %s", questionKey(index))
		}
		verdicts = append(verdicts, Verdict{Claim: claim, Kind: answer.Choice, ClaimProbability: answer.ChoiceProbability(KindClaim)})
	}
	return verdicts, response.Usage, nil
}

func batchState(sources Sources, claims []Claim) map[string]any {
	shown := map[string]shownClaim{}
	for index, claim := range claims {
		shown[questionKey(index)] = shownClaim{At: claim.At, Text: claim.Text}
	}
	state := map[string]any{
		"about":   about,
		"kinds":   kinds,
		"request": strings.Join(sources.Request, "\n\n"),
		"claims":  shown,
	}
	if len(sources.RuntimeFacts) > 0 {
		state["runtimeFacts"] = json.RawMessage(sources.RuntimeFacts)
	}
	if attachments := boundedAttachments(sources.Attachments); len(attachments) > 0 {
		state["attachments"] = attachments
	}
	return state
}

func batchQuestions(count int) map[string]model.DecisionQuestion {
	options := map[string]string{}
	for name := range kinds {
		options[name] = name
	}
	questions := map[string]model.DecisionQuestion{}
	for index := 0; index < count; index++ {
		key := questionKey(index)
		questions[key] = model.ChoiceQuestion{
			Instructions:       fmt.Sprintf("Which kind of value is claims.%s? %s Judge only claims.%s. The kinds are defined in kinds.", key, guard, key),
			OptionDescriptions: options,
		}.Question()
	}
	return questions
}

func questionKey(index int) string {
	return fmt.Sprintf("claim%d", index)
}

func boundedAttachments(attachments []Attachment) []Attachment {
	bounded := []Attachment{}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.Text) == "" {
			continue
		}
		runes := []rune(attachment.Text)
		bounded = append(bounded, Attachment{Name: attachment.Name, Text: string(runes[:min(len(runes), attachmentMaxRunes)])})
	}
	return bounded
}

func copySources(sources Sources) []string {
	copies := append([]string{}, sources.Request...)
	for _, attachment := range sources.Attachments {
		copies = append(copies, attachment.Text)
	}
	var facts any
	if json.Unmarshal(sources.RuntimeFacts, &facts) == nil {
		copies = append(copies, stringValues(facts)...)
	}
	return copies
}

func isCopiedFrom(text string, sources []string) bool {
	wanted := collapsedSpace(text)
	if wanted == "" {
		return true
	}
	for _, source := range sources {
		if strings.Contains(collapsedSpace(source), wanted) {
			return true
		}
	}
	return false
}

func collapsedSpace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		values := []string{}
		for _, item := range typed {
			values = append(values, stringValues(item)...)
		}
		return values
	case map[string]any:
		values := []string{}
		for _, item := range typed {
			values = append(values, stringValues(item)...)
		}
		return values
	}
	return nil
}

func addedUsage(total model.Usage, added model.Usage) model.Usage {
	total.PromptTokens += added.PromptTokens
	total.CompletionTokens += added.CompletionTokens
	total.TotalTokens += added.TotalTokens
	total.CostUSD += added.CostUSD
	return total
}
