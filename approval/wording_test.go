package approval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestAnEditsSpanReachesTheWordingModel(t *testing.T) {
	tool := toolcontract.ToolDefinition{ApprovalInputFields: []string{"oldText", "newText"}}
	toolInput := json.RawMessage(`{"messageID":"m-1","oldText":"금요일","newText":"목요일"}`)

	details := actionDetails(tool, toolInput)

	if string(details["oldText"]) != `"금요일"` || string(details["newText"]) != `"목요일"` || len(details) != 2 {
		t.Fatalf("expected the edited span to travel, got %v", details)
	}
}

func TestOnlyTheInputsAToolDeclaresDescribeItsAction(t *testing.T) {
	tool := toolcontract.ToolDefinition{ApprovalInputFields: []string{"command"}}
	toolInput := json.RawMessage(`{"command":"curl -L https://example.test/a.jpg","approvalReason":"번역해 게시하기 위해 필요합니다"}`)

	details := actionDetails(tool, toolInput)

	if _, isPresent := details["approvalReason"]; isPresent {
		t.Fatalf("an input the tool did not declare is not what the action does, got %v", details)
	}
	if _, isPresent := details["command"]; !isPresent {
		t.Fatalf("a declared input describes the action, got %v", details)
	}
}

func TestAToolDeclaringNoInputsIsDescribedByWholeInput(t *testing.T) {
	toolInput := json.RawMessage(`{"channelName":"잡담","message":"안녕하세요"}`)

	details := actionDetails(toolcontract.ToolDefinition{}, toolInput)

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
