package claimcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	ClaimThreshold     = 0.5
	claimsPerCall      = 60
	attachmentMaxRunes = 6000
)

const (
	KindSource        = "source"
	KindDerived       = "derived"
	KindExpression    = "expression"
	KindUnsupported   = "unsupported"
	KindMistake       = "mistake"
	KindError         = "error"
	KindSlop          = "slop"
	KindContradicted  = "contradicted"
	KindOverstated    = "overstated"
	KindMisattributed = "misattributed"
	kindClaimToday    = "claim"
)

type Treatment string

const (
	TreatmentBlank   Treatment = "blank"
	TreatmentRewrite Treatment = "rewrite"
)

type Profile struct {
	Name       string
	Kinds      map[string]string
	Guard      string
	Treatments map[string]Treatment
	Rewrites   map[string]string
	Fallback   map[string]Treatment
}

func (profile Profile) isDefect(kind string) bool {
	_, isDefect := profile.Treatments[kind]
	return isDefect
}

const about = "The sources are request (the requester's own words), attachments (the text of the files they attached) " +
	"and runtimeFacts (what the system knew when it made the document: today, the requester, the document number and the company profile). " +
	"Each claim is one value the writer put into a delivered document; at says where it sits. All claims belong to one document, so a claim can be checked against the others."

const sourceKind = "says what the request, attachments or runtimeFacts say, in other words, shortened or merged, with the same values, owners and status"
const derivedKind = "follows correctly from the sources by arithmetic or the calendar (a total, a share, a multiple, a duration, a weekday), is a heading or label naming what the sources hold, or summarizes them adding nothing"
const expressionKind = "tone, courtesy, emphasis or an interpretation of facts the sources give, adding no fact of its own: a greeting, thanks, an invitation to get in touch, growth called fast when the sources give the figures, demand called proven when the sources show it"

const guardToday = "Wording, tone, emphasis and formatting are never claim, and whether the tone suits the kind of document is not this question. " +
	"Choose claim only for content the sources do not hold, or hold with a weaker status."

const guardKinds = "Wording, tone, emphasis, formatting and where a value sits in a list are never a defect: choose source, derived or expression when the claim says what the sources say, however differently. " +
	"Choose unsupported for content the sources do not hold, and do not require a claim to be wrong to be unsupported. " +
	"Choose mistake or error only when you can name the exact source value or other claim it conflicts with; do not add a requirement the sources do not state."

const unsupportedKind = "a fact, number, name, customer, ranking, date, promise, condition, cause or availability that no source holds and that no source contradicts; a new obligation; an evaluation of a person or of a quality stated as attested fact"

var (
	TodayProfile = Profile{
		Name: "today",
		Kinds: map[string]string{
			KindSource:     sourceKind,
			KindDerived:    derivedKind,
			KindExpression: expressionKind,
			kindClaimToday: "anything else: a fact, number, name, customer, ranking, date, promise, condition, cause or availability the sources lack; " +
				"a source fact stated with a stronger status (a forecast or target as achieved or confirmed, some as all, planned as done, an estimate or average as exact or guaranteed); " +
				"a new obligation; or an evaluation of a person or of a quality stated as attested fact",
		},
		Guard:      guardToday,
		Treatments: map[string]Treatment{kindClaimToday: TreatmentBlank},
	}
	CompactProfile = Profile{
		Name: "compact",
		Kinds: map[string]string{
			KindSource:      sourceKind,
			KindDerived:     derivedKind,
			KindExpression:  expressionKind,
			KindUnsupported: unsupportedKind,
			KindMistake: "restates something the sources give but gets it wrong: a number, date, name, amount, owner or party that differs from the source, " +
				"or a status or scope the source does not give (a forecast stated as fact, a plan as done, some as all, an estimate as exact)",
			KindError: "derives or reasons from the sources and gets it wrong: a sum, share, date offset, unit conversion or conclusion that does not follow from the values the sources give, " +
				"or a claim that cannot be true together with another claim of the same document",
			KindSlop: "generic marketing or filler with no content of its own: superlatives and buzzwords (world-class, supercharge, seamless), a forced contrast slogan (Not a feature. A platform.), or a dismissive label such as theater",
		},
		Guard: guardKinds,
		Treatments: map[string]Treatment{
			KindUnsupported: TreatmentBlank, KindMistake: TreatmentBlank, KindError: TreatmentBlank, KindSlop: TreatmentRewrite,
		},
		Rewrites: map[string]string{KindSlop: "Say only the facts this unit holds, plainly: drop superlatives, buzzwords, forced contrast and dismissive labels, and add nothing the unit does not say. If the unit holds no fact, answer with an empty text."},
		Fallback: map[string]Treatment{KindSlop: TreatmentKeep},
	}
	FineProfile = Profile{
		Name: "fine",
		Kinds: map[string]string{
			KindSource:      sourceKind,
			KindDerived:     derivedKind,
			KindExpression:  expressionKind,
			KindUnsupported: unsupportedKind,
			KindContradicted: "states a value that conflicts with what the sources say: a number, date, name or amount that differs, a derived figure that is computed wrongly, " +
				"or a claim that cannot be true together with another claim of the same document",
			KindOverstated:    "a sourced fact with its status or scope changed: a forecast or plan stated as fact or as done, some as all, an estimate as exact",
			KindMisattributed: "a sourced fact tied to the wrong person, party, item or date",
			KindSlop:          "generic marketing or filler with no content of its own: superlatives and buzzwords (world-class, supercharge, seamless), a forced contrast slogan (Not a feature. A platform.), or a dismissive label such as theater",
		},
		Guard: guardKinds,
		Treatments: map[string]Treatment{
			KindUnsupported: TreatmentBlank, KindContradicted: TreatmentBlank, KindMisattributed: TreatmentBlank, KindOverstated: TreatmentRewrite, KindSlop: TreatmentRewrite,
		},
		Rewrites: map[string]string{KindSlop: "Say only the facts this unit holds, plainly: drop superlatives, buzzwords, forced contrast and dismissive labels, and add nothing the unit does not say. If the unit holds no fact, answer with an empty text.", KindOverstated: "Rewrite it so it states the status and scope the sources give: a forecast, plan or estimate as such, some as some."},
		Fallback: map[string]Treatment{KindSlop: TreatmentKeep},
	}
)

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
	Kind          string             `json:"kind"`
	Defect        string             `json:"defect,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	IsCopied      bool               `json:"copied,omitempty"`
	Treatment     Treatment          `json:"treatment,omitempty"`
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
	return JudgeWith(ctx, CompactProfile, decisionModel, sources, claims)
}

func JudgeWith(ctx context.Context, profile Profile, decisionModel model.DecisionModel, sources Sources, claims []Claim) (Judgment, error) {
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
		verdicts, usage, errorValue := judgeBatch(ctx, profile, decisionModel, sources, batch)
		if errorValue != nil {
			return Judgment{}, errorValue
		}
		judgment.Verdicts = append(judgment.Verdicts, verdicts...)
		judgment.Usage = addedUsage(judgment.Usage, usage)
	}
	return judgment, nil
}

func (judgment Judgment) Flagged() []Verdict {
	flagged := []Verdict{}
	for _, verdict := range judgment.Verdicts {
		if verdict.Defect != "" {
			flagged = append(flagged, verdict)
		}
	}
	return flagged
}

func (judgment Judgment) Treated(treatment Treatment) []Verdict {
	treated := []Verdict{}
	for _, verdict := range judgment.Flagged() {
		if verdict.Treatment == treatment {
			treated = append(treated, verdict)
		}
	}
	return treated
}

func (profile Profile) defectOf(probabilities map[string]float64, threshold float64) string {
	total, strongest, strongestKind := 0.0, 0.0, ""
	for _, kind := range sortedKinds(profile.Treatments) {
		probability := probabilities[kind]
		total += probability
		if probability > strongest {
			strongest, strongestKind = probability, kind
		}
	}
	if total < threshold {
		return ""
	}
	return strongestKind
}

func sortedKinds(treatments map[string]Treatment) []string {
	kinds := make([]string, 0, len(treatments))
	for kind := range treatments {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func (profile Profile) VerdictFor(claim Claim, choice string, probabilities map[string]float64) Verdict {
	verdict := Verdict{Claim: claim, Kind: choice, Probabilities: probabilities}
	verdict.Defect = profile.defectOf(probabilities, ClaimThreshold)
	verdict.Treatment = profile.Treatments[verdict.Defect]
	return verdict
}

func judgeBatch(ctx context.Context, profile Profile, decisionModel model.DecisionModel, sources Sources, claims []Claim) ([]Verdict, model.Usage, error) {
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: batchState(profile, sources, claims), Questions: batchQuestions(profile, len(claims))})
	if errorValue != nil {
		return nil, model.Usage{}, errorValue
	}
	verdicts := make([]Verdict, 0, len(claims))
	for index, claim := range claims {
		answer, isAnswered := response.Answers[questionKey(index)]
		if !isAnswered {
			return nil, model.Usage{}, fmt.Errorf("the decision model gave no answer for %s", questionKey(index))
		}
		verdicts = append(verdicts, profile.VerdictFor(claim, answer.Choice, answer.Probabilities))
	}
	return verdicts, response.Usage, nil
}

func batchState(profile Profile, sources Sources, claims []Claim) map[string]any {
	shown := map[string]shownClaim{}
	for index, claim := range claims {
		shown[questionKey(index)] = shownClaim{At: claim.At, Text: claim.Text}
	}
	state := map[string]any{
		"about":   about,
		"kinds":   profile.Kinds,
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

func batchQuestions(profile Profile, count int) map[string]model.DecisionQuestion {
	options := map[string]string{}
	for name := range profile.Kinds {
		options[name] = name
	}
	questions := map[string]model.DecisionQuestion{}
	for index := 0; index < count; index++ {
		key := questionKey(index)
		questions[key] = model.ChoiceQuestion{
			Instructions:       fmt.Sprintf("Which kind of value is claims.%s? %s Judge only claims.%s. The kinds are defined in kinds.", key, profile.Guard, key),
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
