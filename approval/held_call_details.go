package approval

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

var targetDetailNames = []string{"resolvedTarget", "resolvedTargetStartsAt", "targetPreview"}

func heldCallDetails(facts holdrecord.QuestionFacts) string {
	details := actionDetails(facts)
	lines := []string{}
	for _, name := range detailNamesInShowingOrder(details) {
		lines = append(lines, detailLines(name, details[name])...)
	}
	return strings.Join(lines, "\n")
}

func detailNamesInShowingOrder(details map[string]json.RawMessage) []string {
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

func detailLines(name string, value json.RawMessage) []string {
	text := detailText(value)
	if !strings.Contains(text, "\n") {
		return []string{name + ": " + text}
	}
	lines := []string{name + ":"}
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
