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

func printedDetailNames(facts holdrecord.QuestionFacts, details map[string]json.RawMessage) []string {
	names := []string{}
	if isHandoff(facts) {
		return names
	}
	for _, name := range append(slices.Clone(targetDetailNames), facts.Tool.ApprovalQuotedFields...) {
		if value, isPresent := details[name]; isPresent && strings.TrimSpace(detailText(value)) != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func heldCallDetails(facts holdrecord.QuestionFacts, labels map[string]string) string {
	details := actionDetails(facts)
	lines := []string{}
	for _, name := range printedDetailNames(facts, details) {
		lines = append(lines, detailLines(labels[name], printedText(facts, name, details[name]))...)
	}
	return strings.Join(lines, "\n")
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

func printedText(facts holdrecord.QuestionFacts, name string, value json.RawMessage) string {
	if name == resolvedTargetStartsAtDetail {
		return momentForPerson(detailText(value), facts.ResponseLanguage, facts.RequesterTimeZone)
	}
	return detailText(value)
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
