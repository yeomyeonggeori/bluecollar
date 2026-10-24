package approval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestAnEditsSpanReachesTheWordingModel(t *testing.T) {
	tool := toolcontract.ToolDefinition{ApprovalInputFields: []string{"oldText", "newText"}}
	toolInput := json.RawMessage(`{"messageID":"m-1","oldText":"금요일","newText":"목요일"}`)

	details := actionDetails(holdrecord.QuestionFacts{Tool: tool, Input: toolInput})

	if string(details["oldText"]) != `"금요일"` || string(details["newText"]) != `"목요일"` || len(details) != 2 {
		t.Fatalf("expected the edited span to travel, got %v", details)
	}
}

func TestOnlyTheInputsAToolDeclaresDescribeItsAction(t *testing.T) {
	tool := toolcontract.ToolDefinition{ApprovalInputFields: []string{"command"}}
	toolInput := json.RawMessage(`{"command":"curl -L https://example.test/a.jpg","approvalReason":"번역해 게시하기 위해 필요합니다"}`)

	details := actionDetails(holdrecord.QuestionFacts{Tool: tool, Input: toolInput})

	if _, isPresent := details["approvalReason"]; isPresent {
		t.Fatalf("an input the tool did not declare is not what the action does, got %v", details)
	}
	if _, isPresent := details["command"]; !isPresent {
		t.Fatalf("a declared input describes the action, got %v", details)
	}
}

func TestAToolDeclaringNoInputsIsDescribedByWholeInput(t *testing.T) {
	toolInput := json.RawMessage(`{"channelName":"잡담","message":"안녕하세요"}`)

	details := actionDetails(holdrecord.QuestionFacts{Input: toolInput})

	if len(details) != 2 {
		t.Fatalf("with nothing declared the whole input is shown, got %v", details)
	}
}

func TestTheWordingModelIsToldTheQuestionCoversThisOneAction(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "일정을 지울까요?"}
	fixture := newFixtureWith(t, languageModel, &scriptedAsker{})

	fixture.awaitOutcome(fixture.request())

	prompt := languageModel.promptSeen()
	if !strings.Contains(prompt, "The question covers this one action and nothing after it.") || strings.Contains(prompt, "approvalScope") {
		t.Fatalf("an approval answers one call, so the question must not offer more, got %s", prompt)
	}
}

func hostFactsFixture() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: "en",
		ModelDraft:       "Shall I delete tomorrow's sync?",
		Tool:             toolcontract.ToolDefinition{Name: "calendar_event_delete"},
		Input:            json.RawMessage(`{"eventHint":"sync","approvalRequired":true}`),
		Target:           agentcontract.ApprovalTarget{InputField: "eventHint", ID: "event-7", Title: "Team sync", Preview: "Tomorrow 10:00, 6 attendees"},
		Choices:          []holdrecord.Choice{{Key: "1", Label: "Now"}, {Key: "2", Label: "Tomorrow 9:00", StartsAt: "2026-10-07T09:00:00+09:00"}},
	}
}

func TestAResolvedTargetReplacesTheSearchPhraseTheCallerTyped(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "Delete Team sync?"}

	NewWorder(languageModel).WordQuestion(context.Background(), hostFactsFixture())

	prompt := languageModel.promptSeen()
	if strings.Contains(prompt, `"eventHint"`) || !strings.Contains(prompt, `"resolvedTarget":"Team sync"`) || !strings.Contains(prompt, `"targetPreview":"Tomorrow 10:00, 6 attendees"`) {
		t.Fatalf("a host that resolved the target hands the harness the event, not the phrase that found it, got %s", prompt)
	}
}

func TestTheChoicesAndTheModelDraftTravelWithTheFacts(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "Delete Team sync?"}

	NewWorder(languageModel).WordQuestion(context.Background(), hostFactsFixture())

	prompt := languageModel.promptSeen()
	for _, expectedFragment := range []string{`"modelDraft":"Shall I delete tomorrow's sync?"`, `"choices":[{"key":"1","label":"Now"},{"key":"2","startsAt":"2026-10-07T09:00:00+09:00","label":"Tomorrow 9:00"}]`} {
		if !strings.Contains(prompt, expectedFragment) {
			t.Fatalf("expected %q in %s", expectedFragment, prompt)
		}
	}
}

func TestAWordingThatFailsReturnsTheRawSummaryAndTheFailure(t *testing.T) {
	languageModel := &wordingLanguageModel{failure: errors.New("model unreachable")}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), hostFactsFixture())

	if wording.Failure == nil || !strings.HasPrefix(wording.Text, "calendar_event_delete Team sync\n\nresolvedTarget: Team sync\n") {
		t.Fatalf("a failed wording still names the resolved target and reports why, got %+v", wording)
	}
}

func heldMessageFacts() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: "ko",
		OriginalRequest:  "박예시한테 내일 회의 시간 물어봐줘",
		Tool:             toolcontract.ToolDefinition{Name: "message_send"},
		Input:            json.RawMessage(`{"targetType":"directMessage","personHint":"박예시","message":"내일 회의를 10시로 옮겨도 될까요?\n안 되면 알려주세요."}`),
		Target:           agentcontract.ApprovalTarget{InputField: "personHint", ID: "person-2", Title: "박예시 (sample2@example.com)"},
	}
}

func TestTheQuestionShowsTheRecipientAndTheExactContentWhateverTheModelWrites(t *testing.T) {
	for _, languageModel := range []*wordingLanguageModel{
		{question: "보낼까요?"},
		{question: "최견본님께 회의 취소를 알릴까요?"},
		{failure: errors.New("the model is down")},
	} {
		wording := NewWorder(languageModel).WordQuestion(context.Background(), heldMessageFacts())

		for _, expectedLine := range []string{
			"resolvedTarget: 박예시 (sample2@example.com)",
			"> 내일 회의를 10시로 옮겨도 될까요?",
			"> 안 되면 알려주세요.",
			"targetType: directMessage",
		} {
			if !strings.Contains(wording.Text, expectedLine) {
				t.Fatalf("the held call's own %q is missing from the question:\n%s", expectedLine, wording.Text)
			}
		}
		if strings.Contains(wording.Text, "personHint") || strings.Contains(wording.Text, "person-2") {
			t.Fatalf("the search phrase or the identifier was shown instead of the resolved recipient:\n%s", wording.Text)
		}
	}
}

func TestTheShownCallIsTheSameForTheSameHeldCall(t *testing.T) {
	first := heldCallDetails(heldMessageFacts())
	second := heldCallDetails(heldMessageFacts())

	if first != second || first == "" {
		t.Fatalf("one held call rendered two ways:\n%s\n---\n%s", first, second)
	}
	if !strings.HasPrefix(first, "resolvedTarget: ") {
		t.Fatalf("the recipient is not the first thing shown:\n%s", first)
	}
}

func TestTheWordingModelWritesOnlyTheAskingSentence(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "박예시님께 보낼까요?"}

	NewWorder(languageModel).WordQuestion(context.Background(), heldMessageFacts())

	if !strings.Contains(languageModel.promptSeen(), "Never quote, paraphrase, or repeat") {
		t.Fatalf("the model is not told the details are shown beneath its sentence:\n%s", languageModel.promptSeen())
	}
}

func handoffFacts() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: "ko",
		OriginalRequest:  "거래처 포털에서 지난달 청구서 받아줘",
		Tool:             toolcontract.ToolDefinition{Name: "browser_handoff"},
		Input:            json.RawMessage(`{"reason":"Sign in to the vendor portal"}`),
		Target:           agentcontract.ApprovalTarget{InputField: "handoff", ID: "handoff-1", Title: "https://portal.example.com", HandoffID: "handoff-1"},
	}
}

func TestAHandoffIsWordedAsAHandOverAndShowsNoCallDetails(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "아래 링크를 열어 거래처 포털에 직접 로그인한 뒤 돌려주세요."}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), handoffFacts())

	prompt := languageModel.promptSeen()
	if !strings.Contains(prompt, "hands the requester the agent's browser") || strings.Contains(prompt, "Write exactly one user-facing approval question") {
		t.Fatalf("a handoff is not an approval question, got %s", prompt)
	}
	if !strings.Contains(prompt, `"reason":"Sign in to the vendor portal"`) || !strings.Contains(prompt, `"resolvedTarget":"https://portal.example.com"`) {
		t.Fatalf("the wording model needs the reason and the site, got %s", prompt)
	}
	if wording.Text != "아래 링크를 열어 거래처 포털에 직접 로그인한 뒤 돌려주세요." {
		t.Fatalf("a handoff message carries no approval details beneath it, got %q", wording.Text)
	}
}
