//go:build llmeval

package approval

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestLiveApprovalQuestionForAnEnglishRequesterIsEnglish(t *testing.T) {
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
	for _, responseLanguage := range []string{"en", ""} {
		assertEnglishApprovalQuestion(t, provider, responseLanguage)
	}
}

func assertEnglishApprovalQuestion(t *testing.T, provider model.LanguageModelProvider, responseLanguage string) {
	t.Helper()
	facts := holdrecord.QuestionFacts{
		ResponseLanguage: responseLanguage,
		OriginalRequest:  "Example Ventures asked for our company materials. Send Sam Sample a data room link.",
		Tool:             toolcontract.ToolDefinition{Name: "dataroom_link_add", ApprovalInputFields: []string{"circleID", "label", "canDownload", "lifetimeHours"}},
		Input:            json.RawMessage(`{"circleID":"investor","label":"Company materials for Sam Sample (Example Ventures)","canDownload":true,"lifetimeHours":72}`),
	}
	for attempt := 1; attempt <= 3; attempt++ {
		wording := NewWorder(provider).WordQuestion(context.Background(), facts)
		if wording.Failure != nil {
			t.Fatalf("attempt %d: the wording model failed: %v", attempt, wording.Failure)
		}
		question := strings.SplitN(wording.Text, "\n\n", 2)[0]
		t.Logf("language %q attempt %d: %q", responseLanguage, attempt, question)
		if strings.ContainsFunc(question, isHangul) {
			t.Errorf("attempt %d: an English requester was asked in Korean: %q", attempt, question)
		}
		if !strings.Contains(question, "Sam Sample") {
			t.Errorf("attempt %d: the recipient's name did not survive as written: %q", attempt, question)
		}
	}
}

func isHangul(character rune) bool {
	return unicode.Is(unicode.Hangul, character)
}
