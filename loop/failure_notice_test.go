package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestElapsedLimitRawErrorSummaryDoesNotClaimSavedProgress(t *testing.T) {
	if strings.Contains(ElapsedLimitRawErrorSummary, "saved") || strings.Contains(ElapsedLimitRawErrorSummary, "continuation") {
		t.Fatalf("expected neutral elapsed limit summary, got %q", ElapsedLimitRawErrorSummary)
	}
}

func TestFinishMessageCompressionPromptUsesMattermostBudget(t *testing.T) {
	prompt := BuildFinishMessageCompressionPrompt("긴 결과입니다.", toolcontract.ResponseLanguageKorean, agentcontract.FinishMessageMaximumCharacters)

	if !strings.Contains(prompt, "Maximum characters: 1200") {
		t.Fatalf("expected Mattermost finish budget, got %q", prompt)
	}
}
