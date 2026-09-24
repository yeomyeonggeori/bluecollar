package agentcontract

import (
	"strings"
	"testing"
)

func TestAReplyFollowsTheRequesterUnlessALanguageIsNamed(t *testing.T) {
	cases := map[string]string{
		"":        "in the language the requester wrote in.",
		"other":   "in the language the requester wrote in.",
		"klingon": "in the language the requester wrote in.",
		"en":      "in English.",
		"Korean":  "in Korean.",
		"ja":      "in Japanese.",
		"TURKISH": "in Turkish.",
	}
	for namedLanguage, expectedEnding := range cases {
		instruction := ResponseLanguageInstruction(namedLanguage)
		if !strings.Contains(instruction, expectedEnding) {
			t.Errorf("language %q gave %q, want it to say %q", namedLanguage, instruction, expectedEnding)
		}
	}
}
