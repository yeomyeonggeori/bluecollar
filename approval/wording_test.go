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

func TestTheWordingModelIsToldWhatAnApprovalScopeCovers(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "일정을 지울까요?"}
	fixture := newFixtureWith(t, languageModel, &scriptedAsker{})
	request := fixture.scopedRequest()
	request.toolDefinition.ApprovalScopeSummary = "every change to the team calendar"

	fixture.awaitOutcome(request)

	prompt := languageModel.promptSeen()
	for _, expectedFragment := range []string{`"approvalScope":{"name":"calendar","covers":"every change to the team calendar"}`, "saying yes approves every action of that scope"} {
		if !strings.Contains(prompt, expectedFragment) {
			t.Fatalf("approving a scoped call approves the scope, so the model must be told what it covers, expected %q in %s", expectedFragment, prompt)
		}
	}
}

func TestAnUnscopedToolIsNotWordedAsGrantingAScope(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "일정을 지울까요?"}
	fixture := newFixtureWith(t, languageModel, &scriptedAsker{})

	fixture.awaitOutcome(fixture.request())

	if strings.Contains(languageModel.lastRequest.Messages[2].Content, "approvalScope") {
		t.Fatalf("a call with no scope has none to describe, got %s", languageModel.lastRequest.Messages[2].Content)
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

	if wording.Failure == nil || wording.Text != "calendar_event_delete Team sync" {
		t.Fatalf("a failed wording still names the resolved target and reports why, got %+v", wording)
	}
}
