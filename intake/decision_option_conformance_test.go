package intake

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestTheOutputFormatQuestionsAreTheFormatsTheRuntimeAccepts(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("보고서 정리해줘"))
	askedFormats := []string{}
	for questionName := range questions {
		if formatName, isFormat := strippedQuestionPrefix(questionName, "m1."+agentcontract.IntakeQuestionPrefixFormat); isFormat {
			askedFormats = append(askedFormats, formatName)
			if !agentcontract.IsRequestedOutputFormatName(formatName) {
				t.Fatalf("the decision asks about %q, which the runtime does not accept as an output format", formatName)
			}
		}
	}
	if len(askedFormats) != len(agentcontract.RequestedOutputFormatNames) {
		t.Fatalf("expected one question per accepted output format, got %v", askedFormats)
	}
}

func strippedQuestionPrefix(value string, prefix string) (string, bool) {
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return "", false
	}
	return value[len(prefix):], true
}
