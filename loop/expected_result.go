package loop

import (
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"regexp"
	"strings"
)

var observedURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

func missingObservedURLInReply(toolSet *toolcontract.ToolSet, observations []turnObservation, finishMessage string) string {
	observedURLs := observedFactURLs(observedFactsFromObservations(toolSet, changingObservations(toolSet, observations)), nil)
	if len(observedURLs) == 0 || finishMessageContainsObservedURL(finishMessage, observedURLs) {
		return ""
	}
	return "final message must include this exact observed URL: " + strings.Join(observedURLs, " ")
}

func observedFactURLs(facts []ObservedFact, requiredEffects []OutcomeEffect) []string {
	urls := []string{}
	for _, fact := range facts {
		if len(requiredEffects) > 0 && !factMatchesAnyRequiredEffect(fact, requiredEffects) {
			continue
		}
		if normalizedURL := normalizeObservedURL(fact.URL); normalizedURL != "" {
			urls = appendUniqueStrings(urls, normalizedURL)
		}
	}
	return urls
}

func factMatchesAnyRequiredEffect(fact ObservedFact, requiredEffects []OutcomeEffect) bool {
	for _, requiredEffect := range requiredEffects {
		if fact.ObjectType == requiredEffect.ObjectType && fact.Effect == requiredEffect.Effect {
			return true
		}
	}
	return false
}

func finishMessageContainsObservedURL(finishMessage string, observedURLs []string) bool {
	messageURLs := observedURLsFromText(finishMessage)
	for _, observedURL := range observedURLs {
		if stringSliceContains(messageURLs, normalizeObservedURL(observedURL)) {
			return true
		}
	}
	return false
}

func observedURLsFromText(value string) []string {
	urls := []string{}
	for _, match := range observedURLPattern.FindAllString(value, -1) {
		urls = appendUniqueStrings(urls, normalizeObservedURL(match))
	}
	return urls
}

func normalizeObservedURL(value string) string {
	normalizedURL := strings.TrimRight(strings.TrimSpace(value), ".,);:!?")
	return strings.TrimRight(normalizedURL, "/")
}

func changingObservations(toolSet *toolcontract.ToolSet, observations []turnObservation) []turnObservation {
	changing := []turnObservation{}
	for _, observation := range observations {
		definition, isFound := toolSet.ToolDefinition(observation.Tool)
		if isFound && toolcontract.ToolDefinitionSideEffectClass(definition) == toolcontract.ToolSideEffectRead {
			continue
		}
		changing = append(changing, observation)
	}
	return changing
}
