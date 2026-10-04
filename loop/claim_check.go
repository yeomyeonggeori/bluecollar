package loop

import (
	"fmt"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	claimSupportedThreshold        = 0.4
	claimRoundLimit                = 3
	claimDocumentLimit             = 3
	claimsPerDocumentLimit         = 40
	attachmentPreviewMaximumLength = 6000
)

const claimRule = "The sources are request and conversationBefore (the requester's own words), attachments (the text of the files they attached) " +
	"and runtimeFacts (what the system knew when it made each file: today, the requester, the document number and the company profile). " +
	"A claim is one value the writer put into a delivered file; at says where it sits. " +
	"A claim is unsupported when it states as fact something the sources neither say nor imply: an event that happened, " +
	"a cause or reason, a promise or instruction to the reader, an availability or condition, a status, " +
	"or a name, number, date, time, place, amount or rate. Check each such fact in the claim against the sources. " +
	"These are supported and never failures: the sources' facts in other words, merged, shortened or reordered; " +
	"headings, labels, field names left empty, greetings and generic courtesy; values that follow from the sources " +
	"by arithmetic or the calendar (totals, differences, shares, durations, weekdays, amounts in words) and notes saying how a value is computed; " +
	"runtimeFacts values such as the company's name, address, phone, representative and the requester; " +
	"notes saying a value was not given, is not decided, or will be announced later."

type documentClaim struct {
	File       string `json:"file"`
	Path       string `json:"path"`
	At         string `json:"at,omitempty"`
	Text       string `json:"text"`
	IsEnforced bool   `json:"enforced"`
}

type claimShown struct {
	At   string `json:"at,omitempty"`
	Text string `json:"text"`
}

type claimEvidence struct {
	Claims       []documentClaim
	RuntimeFacts map[string]any
}

type unsupportedClaim struct {
	documentClaim
	Noul  float64 `json:"noul"`
	Round int     `json:"round"`
}

func deliveredClaims(request AgentTurnRequest, observations []turnObservation) claimEvidence {
	evidence := claimEvidence{RuntimeFacts: map[string]any{}}
	copies := copySources(request)
	for _, attachment := range deliveredSourcesNewestFirst(observations) {
		if len(evidence.RuntimeFacts) == claimDocumentLimit {
			break
		}
		known := decodedJSON(attachment.Source.Known)
		evidence.RuntimeFacts[attachment.DevicePath] = known
		documentCopies := append(append([]string{}, copies...), stringValues(known)...)
		evidence.Claims = append(evidence.Claims, claimsToAsk(attachment, documentCopies)...)
	}
	return evidence
}

func claimsToAsk(attachment toolcontract.FileAttachment, copies []string) []documentClaim {
	asked := []documentClaim{}
	for _, claim := range attachment.Source.Claims {
		if len(asked) == claimsPerDocumentLimit {
			break
		}
		if strings.TrimSpace(claim.Text) == "" || isCopiedFrom(claim.Text, copies) {
			continue
		}
		asked = append(asked, documentClaim{File: attachment.DevicePath, Path: claim.Path, At: claim.At, Text: claim.Text, IsEnforced: attachment.Source.IsLayoutOwnedByCode})
	}
	return asked
}

func deliveredSourcesNewestFirst(observations []turnObservation) []toolcontract.FileAttachment {
	sources := []toolcontract.FileAttachment{}
	isSeen := map[string]bool{}
	successful := successfulToolObservations(observations)
	for index := len(successful) - 1; index >= 0; index-- {
		if !toolcontract.IsArtifactDeliveryTool(successful[index].Tool) {
			continue
		}
		for _, attachment := range successful[index].Attachments {
			devicePath := strings.TrimSpace(attachment.DevicePath)
			if attachment.Source == nil || devicePath == "" || isSeen[devicePath] {
				continue
			}
			isSeen[devicePath] = true
			sources = append(sources, attachment)
		}
	}
	return sources
}

func copySources(request AgentTurnRequest) []string {
	sources := append(append([]string{}, requestWordings(request)...), conversationBeforeRequest(request)...)
	for _, attachment := range attachmentPreviews(request) {
		sources = append(sources, attachment.Text)
	}
	return sources
}

func isCopiedFrom(text string, sources []string) bool {
	wanted := collapsedSpace(text)
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
		return nonEmptyStrings([]string{typed})
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

type attachmentPreview struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func attachmentPreviews(request AgentTurnRequest) []attachmentPreview {
	previews := []attachmentPreview{}
	for _, part := range request.InputParts {
		if part.Type != agentcontract.AgentPartTypeFile || part.File == nil || strings.TrimSpace(part.File.MarkdownPreview) == "" {
			continue
		}
		bounded, _ := boundedRunes(part.File.MarkdownPreview, attachmentPreviewMaximumLength)
		previews = append(previews, attachmentPreview{Name: firstNonEmptyString(part.File.Filename, part.File.Path), Text: bounded})
	}
	return previews
}

func boundedRunes(text string, maximumRunes int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= maximumRunes {
		return text, false
	}
	return string(runes[:maximumRunes]), true
}

func claimQuestionKey(index int) string {
	return fmt.Sprintf("claim%d", index)
}

func claimQuestions(claims []documentClaim) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index := range claims {
		key := claimQuestionKey(index)
		questions[key] = model.NoulQuestion{
			Instructions:     fmt.Sprintf("Is claims.%s supported by the sources, under claimRule? Judge only claims.%s.", key, key),
			TrueDescription:  "supported: everything it states is in the sources or follows from them",
			FalseDescription: "unsupported: it states a fact the sources do not contain or entail",
		}.Question()
	}
	return questions
}

func addClaimState(state map[string]any, request AgentTurnRequest, evidence claimEvidence) {
	if len(evidence.Claims) == 0 {
		return
	}
	claims := map[string]claimShown{}
	for index, claim := range evidence.Claims {
		claims[claimQuestionKey(index)] = claimShown{At: claim.At, Text: claim.Text}
	}
	state["claims"] = claims
	state["claimRule"] = claimRule
	state["runtimeFacts"] = evidence.RuntimeFacts
	if attachments := attachmentPreviews(request); len(attachments) > 0 {
		state["attachments"] = attachments
	}
}

type judgedClaim struct {
	documentClaim
	Noul float64 `json:"noul"`
}

func judgedClaims(response model.DecisionResponse, claims []documentClaim) []judgedClaim {
	judged := []judgedClaim{}
	for index, claim := range claims {
		if answer, isAnswered := response.Answers[claimQuestionKey(index)]; isAnswered {
			judged = append(judged, judgedClaim{documentClaim: claim, Noul: answer.Noul})
		}
	}
	return judged
}

type claimLedger struct {
	mutex    sync.Mutex
	support  map[documentClaim]float64
	refusals map[string]int
}

func newClaimLedger() *claimLedger {
	return &claimLedger{support: map[documentClaim]float64{}, refusals: map[string]int{}}
}

func (ledger *claimLedger) unjudged(claims []documentClaim) []documentClaim {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	pending := []documentClaim{}
	for _, claim := range claims {
		if _, isJudged := ledger.support[claim]; !isJudged {
			pending = append(pending, claim)
		}
	}
	return pending
}

func (ledger *claimLedger) record(judged []judgedClaim) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	for _, claim := range judged {
		ledger.support[claim.documentClaim] = claim.Noul
	}
}

func (ledger *claimLedger) refuse(claims []documentClaim) (refused []unsupportedClaim, abandoned []unsupportedClaim) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	for _, claim := range claims {
		noul, isJudged := ledger.support[claim]
		if !claim.IsEnforced || !isJudged || noul >= claimSupportedThreshold {
			continue
		}
		location := claim.File + "\x00" + claim.Path
		ledger.refusals[location]++
		unsupported := unsupportedClaim{documentClaim: claim, Noul: noul, Round: ledger.refusals[location]}
		if unsupported.Round > claimRoundLimit {
			abandoned = append(abandoned, unsupported)
			continue
		}
		refused = append(refused, unsupported)
	}
	return refused, abandoned
}

func (agentTurnRunner *AgentTurnRunner) claimLedgerFor(taskRunID string) *claimLedger {
	if agentTurnRunner.claimLedgers == nil {
		return newClaimLedger()
	}
	stored, _ := agentTurnRunner.claimLedgers.LoadOrStore(taskRunID, newClaimLedger())
	return stored.(*claimLedger)
}

func unsupportedClaimLines(claims []unsupportedClaim) []string {
	lines := []string{}
	for _, claim := range claims {
		instruction := "change only this value so it says what the sources support, or set it to null so it is a blank the reply offers to complete, then make the file again"
		if claim.Round == claimRoundLimit {
			instruction = "set this value to null so it is a blank the reply offers to complete, then make the file again"
		}
		lines = append(lines, fmt.Sprintf("%q at %s in %s: the request, its attachments and the runtime facts do not support it; %s", claim.Text, claim.Path, claim.File, instruction))
	}
	return lines
}
