//go:build llmeval

package approval

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var numberedLine = regexp.MustCompile(`(?m)^\s*\d+\s*[.)]`)

func liveWordingProvider(t *testing.T) model.LanguageModelProvider {
	t.Helper()
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
	return provider
}

func TestLiveAnApprovalWithNoChoicesListsNoOptions(t *testing.T) {
	worder := NewWorder(liveWordingProvider(t))
	facts := holdrecord.QuestionFacts{
		ResponseLanguage: "ko",
		Tool:             toolcontract.ToolDefinition{Name: "message_send", ApprovalQuotedFields: []string{"message", "attachments"}},
		Input:            json.RawMessage(`{"targetType":"directMessage","personHint":"박예시","message":"테스트"}`),
		Target:           agentcontract.ApprovalTarget{InputField: "personHint", ID: "person-2", Title: "박예시"},
	}
	for attempt := 1; attempt <= 10; attempt++ {
		worded, errorValue := worder.generateQuestion(context.Background(), facts)
		if errorValue != nil {
			t.Fatalf("attempt %d: the wording model failed: %v", attempt, errorValue)
		}
		t.Logf("attempt %d: %q labels %q", attempt, worded.Question, worded.Labels)
		if numberedLine.MatchString(worded.Question) {
			t.Errorf("attempt %d: an approval with no choices was worded with a numbered list of options: %q", attempt, worded.Question)
		}
		if containsAny(worded.Question, "테스트", "박예시") {
			t.Errorf("attempt %d: the question repeats a value printed beneath it: %q", attempt, worded.Question)
		}
		assertLabelsArePlainWords(t, worded.Labels, []string{"resolvedTarget", "message"})
	}
}

func assertLabelsArePlainWords(t *testing.T, labels map[string]string, printed []string) {
	t.Helper()
	for _, name := range printed {
		label := labels[name]
		if label == "" {
			t.Errorf("the printed value %s has no label: %q", name, labels)
		}
		for _, rawName := range []string{"resolvedTarget", "message", "targetType", "directMessage"} {
			if strings.Contains(label, rawName) {
				t.Errorf("the label for %s reads as a field name: %q", name, label)
			}
		}
	}
}

func TestLiveASettingNobodyQuotesIsStatedInPlainWords(t *testing.T) {
	worder := NewWorder(liveWordingProvider(t))
	facts := holdrecord.QuestionFacts{
		ResponseLanguage: "ko",
		Tool:             toolcontract.ToolDefinition{Name: "dataroom_link_add"},
		Input:            json.RawMessage(`{"circleID":"investor","label":"예시벤처스 회사 자료","canDownload":true,"lifetimeHours":72}`),
	}
	for attempt := 1; attempt <= 5; attempt++ {
		wording := worder.WordQuestion(context.Background(), facts)
		if wording.Failure != nil {
			t.Fatalf("attempt %d: the wording model failed: %v", attempt, wording.Failure)
		}
		t.Logf("attempt %d: %q", attempt, wording.Text)
		for _, rawName := range []string{"canDownload", "lifetimeHours"} {
			if strings.Contains(wording.Text, rawName) {
				t.Errorf("attempt %d: a setting reached the requester by its field name %s: %q", attempt, rawName, wording.Text)
			}
		}
		if !containsAny(wording.Text, "다운로드", "내려받") || !containsAny(wording.Text, "72", "3일", "사흘") {
			t.Errorf("attempt %d: a setting printed nowhere else is missing from the question: %q", attempt, wording.Text)
		}
	}
}

func containsAny(text string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}
