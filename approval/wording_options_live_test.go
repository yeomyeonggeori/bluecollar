//go:build llmeval

package approval

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var numberedLine = regexp.MustCompile(`(?m)^\s*\d+\s*[.)]`)

func TestLiveAnApprovalWithNoChoicesListsNoOptions(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Fatal("OPENROUTER_API_KEY is required: building with the llmeval tag is the request to run this evaluation")
	}
	modelName := os.Getenv("BLUECOLLAR_MODEL_NAME")
	if modelName == "" {
		modelName = "z-ai/glm-5.3-flash"
	}
	provider, errorValue := (openaicompatible.Endpoint{URL: "https://openrouter.ai/api/v1", ModelName: modelName, APIKey: apiKey}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, modelDraft := range []string{"", "박예시님께 DM으로 '테스트'를 보낼까요?\n\n1. 지금 보내기\n2. 취소"} {
		assertNoOptionsListed(t, NewWorder(provider), modelDraft)
	}
}

func assertNoOptionsListed(t *testing.T, worder Worder, modelDraft string) {
	t.Helper()
	facts := holdrecord.QuestionFacts{
		ResponseLanguage: "ko",
		OriginalRequest:  "박예시한테 '테스트'라고 DM 보내봐",
		ModelDraft:       modelDraft,
		Tool:             toolcontract.ToolDefinition{Name: "message_send"},
		Input:            json.RawMessage(`{"targetType":"directMessage","personHint":"박예시","message":"테스트"}`),
	}
	for attempt := 1; attempt <= 5; attempt++ {
		wording := worder.WordQuestion(context.Background(), facts)
		if wording.Failure != nil {
			t.Fatalf("draft %q attempt %d: the wording model failed: %v", modelDraft, attempt, wording.Failure)
		}
		question := strings.TrimSpace(strings.TrimSuffix(wording.Text, heldCallDetails(facts)))
		t.Logf("draft %q attempt %d: %q", modelDraft, attempt, question)
		if numberedLine.MatchString(question) {
			t.Errorf("draft %q attempt %d: an approval with no choices was worded with a numbered list of options: %q", modelDraft, attempt, question)
		}
	}
}
