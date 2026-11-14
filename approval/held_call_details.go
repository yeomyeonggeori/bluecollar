package approval

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const (
	resolvedTargetDetail         = "resolvedTarget"
	resolvedTargetAddressDetail  = "resolvedTargetAddress"
	resolvedTargetStartsAtDetail = "resolvedTargetStartsAt"
	targetPreviewDetail          = "targetPreview"
)

var targetDetailNames = []string{resolvedTargetDetail, resolvedTargetAddressDetail, resolvedTargetStartsAtDetail, targetPreviewDetail}

func quotedInputName(facts holdrecord.QuestionFacts) string {
	name := strings.TrimSpace(facts.Tool.ApprovalQuotedField)
	if name == "" || isHandoff(facts) || strings.TrimSpace(quotedText(facts, name)) == "" {
		return ""
	}
	return name
}

func quotedText(facts holdrecord.QuestionFacts, name string) string {
	document := map[string]json.RawMessage{}
	if json.Unmarshal(facts.Input, &document) != nil {
		return ""
	}
	return detailText(document[name])
}

func quotation(facts holdrecord.QuestionFacts) string {
	name := quotedInputName(facts)
	if name == "" {
		return ""
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(quotedText(facts, name)), "\n") {
		lines = append(lines, "> "+line)
	}
	return strings.Join(lines, "\n")
}

func detailsForTheQuestion(facts holdrecord.QuestionFacts) map[string]json.RawMessage {
	details := actionDetails(facts)
	delete(details, quotedInputName(facts))
	if startsAt, isPresent := details[resolvedTargetStartsAtDetail]; isPresent {
		addDetail(details, resolvedTargetStartsAtDetail, momentForPerson(detailText(startsAt), facts.ResponseLanguage, facts.RequesterTimeZone))
	}
	if len(details) == 0 {
		return nil
	}
	return details
}

func rawCallDetails(facts holdrecord.QuestionFacts) string {
	if isHandoff(facts) {
		return ""
	}
	details := actionDetails(facts)
	lines := []string{}
	for _, name := range rawDetailNamesInShowingOrder(details) {
		lines = append(lines, detailLines(name, detailText(details[name]))...)
	}
	return strings.Join(lines, "\n")
}

func rawDetailNamesInShowingOrder(details map[string]json.RawMessage) []string {
	names := []string{}
	for _, name := range targetDetailNames {
		if _, isPresent := details[name]; isPresent {
			names = append(names, name)
		}
	}
	inputNames := []string{}
	for name := range details {
		if !slices.Contains(targetDetailNames, name) {
			inputNames = append(inputNames, name)
		}
	}
	slices.Sort(inputNames)
	return append(names, inputNames...)
}

func detailLines(label string, text string) []string {
	if !strings.Contains(text, "\n") {
		return []string{label + ": " + text}
	}
	lines := []string{label + ":"}
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, "> "+line)
	}
	return lines
}

func detailText(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var texts []string
	if json.Unmarshal(value, &texts) == nil {
		return strings.Join(texts, ", ")
	}
	return strings.TrimSpace(string(value))
}
