package approval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestAMomentIsPrintedInTheRequestersLanguageAndTimeZone(t *testing.T) {
	for _, testCase := range []struct {
		language string
		timeZone string
		expected string
	}{
		{language: "ko", timeZone: "Asia/Seoul", expected: "2026년 8월 18일 (화) 오후 2:00"},
		{language: "ko", timeZone: "America/Los_Angeles", expected: "2026년 8월 17일 (월) 오후 10:00"},
		{language: "en", timeZone: "Europe/London", expected: "Tue, Aug 18, 2026, 6:00 AM"},
		{language: "ja", timeZone: "Asia/Tokyo", expected: "2026年8月18日(火) 14:00"},
		{language: "es", timeZone: "Europe/Madrid", expected: "2026-08-18 07:00"},
	} {
		if moment := momentForPerson("2026-08-18T14:00:00+09:00", testCase.language, testCase.timeZone); moment != testCase.expected {
			t.Errorf("%s in %s: got %q, want %q", testCase.language, testCase.timeZone, moment, testCase.expected)
		}
	}
}

func TestAMomentWhoseReaderTimeZoneIsUnknownSaysWhichOffsetItIsIn(t *testing.T) {
	for _, timeZone := range []string{"", "Not/AZone"} {
		if moment := momentForPerson("2026-08-18T14:00:00+09:00", "ko", timeZone); moment != "2026년 8월 18일 (화) 오후 2:00 (UTC+9)" {
			t.Errorf("time zone %q: a wall clock with no zone to read it in names its offset, got %q", timeZone, moment)
		}
	}
	if moment := momentForPerson("2026-08-18T05:30:00+05:30", "en", ""); moment != "Tue, Aug 18, 2026, 5:30 AM (UTC+5:30)" {
		t.Errorf("got %q", moment)
	}
}

func TestATargetsStartIsPrintedForAPersonAndHandedToTheModelAsTheInstant(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "회의를 지울까요?", labels: map[string]string{"resolvedTarget": "일정", "resolvedTargetStartsAt": "시작"}}
	facts := holdrecord.QuestionFacts{
		ResponseLanguage:  "ko",
		RequesterTimeZone: "Asia/Seoul",
		Tool:              toolcontract.ToolDefinition{Name: "task_delete"},
		Input:             json.RawMessage(`{"taskHint":"주간 회의"}`),
		Target:            agentcontract.ApprovalTarget{InputField: "taskHint", ID: "task-1", Title: "주간 회의", StartsAt: "2026-08-18T14:00:00+09:00"},
	}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), facts)

	if wording.Text != "회의를 지울까요?\n\n일정: 주간 회의\n시작: 2026년 8월 18일 (화) 오후 2:00" {
		t.Fatalf("a person reads the start as a date and time, got %q", wording.Text)
	}
	if !strings.Contains(languageModel.promptSeen(), `"resolvedTargetStartsAt":"2026-08-18T14:00:00+09:00"`) {
		t.Fatalf("the wording model reads the exact instant, got %s", languageModel.promptSeen())
	}
}
