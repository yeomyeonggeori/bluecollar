package approval

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
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

func TestAToolDeclaringNoInputsIsDescribedToTheWordingModelByWholeInput(t *testing.T) {
	toolInput := json.RawMessage(`{"channelName":"잡담","message":"안녕하세요"}`)

	details := actionDetails(holdrecord.QuestionFacts{Input: toolInput})

	if len(details) != 2 {
		t.Fatalf("with nothing declared the wording model reads the whole input, got %v", details)
	}
}

func TestAnInputNoToolQuotesIsLeftToTheSentence(t *testing.T) {
	facts := holdrecord.QuestionFacts{
		Tool:  toolcontract.ToolDefinition{Name: "dataroom_link_add"},
		Input: json.RawMessage(`{"circleID":"investor","label":"Company materials","canDownload":true,"lifetimeHours":72}`),
	}

	if details := heldCallDetails(facts, nil); details != "" {
		t.Fatalf("a setting is the sentence's to state in plain words, never printed by its name, got:\n%s", details)
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

func TestTheWordingModelIsShownNoValueThePersonReadsBeneathTheQuestion(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "DM을 보낼까요?", labels: heldMessageLabels}

	NewWorder(languageModel).WordQuestion(context.Background(), heldMessageFacts())

	prompt := languageModel.promptSeen()
	for _, printedValue := range []string{"박예시", "내일 회의를 10시로"} {
		if strings.Contains(prompt, printedValue) {
			t.Fatalf("a value printed beneath the question must have one writer, the runtime, yet the wording model was shown %q in %s", printedValue, prompt)
		}
	}
	if !strings.Contains(prompt, `"printedBelow":["resolvedTarget","message"]`) || !strings.Contains(prompt, `"actionDetails":{"targetType":"directMessage"}`) {
		t.Fatalf("the wording model labels the printed values by name and words every other detail, got %s", prompt)
	}
}

func hostFactsFixture() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: "en",
		Tool:             toolcontract.ToolDefinition{Name: "calendar_event_delete"},
		Input:            json.RawMessage(`{"eventHint":"sync","approvalRequired":true}`),
		Target:           agentcontract.ApprovalTarget{InputField: "eventHint", ID: "event-7", Title: "Team sync", Preview: "Tomorrow 10:00, 6 attendees"},
		Choices:          []holdrecord.Choice{{Key: "1", Label: "Now"}, {Key: "2", Label: "Tomorrow 9:00", StartsAt: "2026-10-07T09:00:00+09:00"}},
	}
}

func TestAResolvedTargetIsPrintedBeneathTheQuestionAndNeverWorded(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "Delete this event?"}

	NewWorder(languageModel).WordQuestion(context.Background(), hostFactsFixture())

	prompt := languageModel.promptSeen()
	if strings.Contains(prompt, "sync") || strings.Contains(prompt, "Tomorrow 10:00") || !strings.Contains(prompt, `"printedBelow":["resolvedTarget","targetPreview"]`) {
		t.Fatalf("the resolved event is printed beneath the question, so neither it nor the phrase that found it reaches the wording model, got %s", prompt)
	}
}

func TestTheChoicesTravelWithTheFacts(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "Delete Team sync?"}

	NewWorder(languageModel).WordQuestion(context.Background(), hostFactsFixture())

	prompt := languageModel.promptSeen()
	for _, expectedFragment := range []string{`"choices":[{"key":"1","label":"Now"},{"key":"2","startsAt":"2026-10-07T09:00:00+09:00","label":"Tomorrow 9:00"}]`} {
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
		Tool:             toolcontract.ToolDefinition{Name: "message_send", ApprovalQuotedFields: []string{"message", "attachments"}},
		Input:            json.RawMessage(`{"targetType":"directMessage","personHint":"박예시","message":"내일 회의를 10시로 옮겨도 될까요?\n안 되면 알려주세요."}`),
		Target:           agentcontract.ApprovalTarget{InputField: "personHint", ID: "person-2", Title: "박예시"},
	}
}

var heldMessageLabels = map[string]string{"resolvedTarget": "받는 사람", "message": "보낼 내용"}

func TestTheQuestionShowsTheRecipientAndTheExactContentUnderTheModelsLabels(t *testing.T) {
	for _, question := range []string{"보낼까요?", "최견본님께 회의 취소를 알릴까요?"} {
		languageModel := &wordingLanguageModel{question: question, labels: heldMessageLabels}

		wording := NewWorder(languageModel).WordQuestion(context.Background(), heldMessageFacts())

		expected := question + "\n\n받는 사람: 박예시\n보낼 내용:\n> 내일 회의를 10시로 옮겨도 될까요?\n> 안 되면 알려주세요."
		if wording.Failure != nil || wording.Text != expected {
			t.Fatalf("the recipient and the exact content are printed beneath any sentence, under the model's labels, got %+v", wording)
		}
	}
}

func TestAFailedWordingShowsTheWholeHeldCallUnderItsOwnNames(t *testing.T) {
	wording := NewWorder(&wordingLanguageModel{failure: errors.New("the model is down")}).WordQuestion(context.Background(), heldMessageFacts())

	expected := "message_send 박예시\n\nresolvedTarget: 박예시\nmessage:\n> 내일 회의를 10시로 옮겨도 될까요?\n> 안 되면 알려주세요.\ntargetType: directMessage"
	if wording.Failure == nil || wording.Text != expected {
		t.Fatalf("with no wording the requester still reads everything the call will do, got %+v", wording)
	}
}

func TestTheShownCallIsTheSameForTheSameHeldCall(t *testing.T) {
	first := heldCallDetails(heldMessageFacts(), heldMessageLabels)
	second := heldCallDetails(heldMessageFacts(), heldMessageLabels)

	if first != second || first == "" {
		t.Fatalf("one held call rendered two ways:\n%s\n---\n%s", first, second)
	}
	if !strings.HasPrefix(first, "받는 사람: ") {
		t.Fatalf("the recipient is not the first thing shown:\n%s", first)
	}
}

func TestTheSchemaAsksForALabelForExactlyThePrintedValues(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "박예시님께 보낼까요?", labels: heldMessageLabels}

	NewWorder(languageModel).WordQuestion(context.Background(), heldMessageFacts())

	var schema struct {
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
		Properties           struct {
			Labels struct {
				Required             []string                   `json:"required"`
				AdditionalProperties bool                       `json:"additionalProperties"`
				Properties           map[string]json.RawMessage `json:"properties"`
			} `json:"labels"`
		} `json:"properties"`
	}
	if errorValue := json.Unmarshal([]byte(languageModel.lastRequest.StructuredOutputSchema.Document), &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	labels := schema.Properties.Labels
	if !slices.Equal(labels.Required, []string{"resolvedTarget", "message"}) || len(labels.Properties) != 2 || labels.AdditionalProperties || schema.AdditionalProperties || !slices.Equal(schema.Required, []string{"question", "labels"}) {
		t.Fatalf("the schema must ask for one label per printed value and nothing else, got %s", languageModel.lastRequest.StructuredOutputSchema.Document)
	}
}

func TestWithNothingPrintedTheSchemaAsksOnlyForTheQuestion(t *testing.T) {
	languageModel := &wordingLanguageModel{question: "Create the link?"}
	facts := holdrecord.QuestionFacts{Tool: toolcontract.ToolDefinition{Name: "dataroom_link_add"}, Input: json.RawMessage(`{"canDownload":true}`)}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), facts)

	if document := languageModel.lastRequest.StructuredOutputSchema.Document; document != `{"additionalProperties":false,"properties":{"question":{"type":"string"}},"required":["question"],"type":"object"}` {
		t.Fatalf("with nothing printed there is nothing to label, got %s", document)
	}
	if !strings.Contains(languageModel.promptSeen(), `"printedBelow":[]`) {
		t.Fatalf("the model must read that nothing is printed beneath its sentence, or it points at a list that is not there, got %s", languageModel.promptSeen())
	}
	if wording.Failure != nil || wording.Text != "Create the link?" {
		t.Fatalf("the sentence alone is the question, got %+v", wording)
	}
}

func TestAMissingOrMultiLineLabelFailsTheWordingTogetherWithTheQuestion(t *testing.T) {
	for name, labels := range map[string]map[string]string{
		"empty":     {"resolvedTarget": " ", "message": "보낼 내용"},
		"two lines": {"resolvedTarget": "받는\n사람", "message": "보낼 내용"},
	} {
		t.Run(name, func(t *testing.T) {
			wording := NewWorder(&wordingLanguageModel{question: "박예시님께 보낼까요?", labels: labels}).WordQuestion(context.Background(), heldMessageFacts())

			if wording.Failure == nil || !strings.HasPrefix(wording.Text, "message_send 박예시\n\nresolvedTarget: 박예시\n") {
				t.Fatalf("a value with no usable label is printed under its own name beneath the raw summary, got %+v", wording)
			}
		})
	}
}

func TestASharedNamesAddressIsPrintedOnALineOfItsOwn(t *testing.T) {
	facts := heldMessageFacts()
	facts.Target.Address = "sample2@example.com"
	labels := map[string]string{"resolvedTarget": "받는 사람", "resolvedTargetAddress": "주소", "message": "보낼 내용"}

	details := heldCallDetails(facts, labels)

	if !strings.HasPrefix(details, "받는 사람: 박예시\n주소: sample2@example.com\n") {
		t.Fatalf("each printed person line must be a hint on its own, never a name and an address in one token, got:\n%s", details)
	}
}

func handoffFacts() holdrecord.QuestionFacts {
	return holdrecord.QuestionFacts{
		ResponseLanguage: "ko",
		Tool:             toolcontract.ToolDefinition{Name: "browser_handoff"},
		Input:            json.RawMessage(`{"step":"sign in to the vendor portal"}`),
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
	if !strings.Contains(prompt, `"step":"sign in to the vendor portal"`) || !strings.Contains(prompt, `"resolvedTarget":"https://portal.example.com"`) {
		t.Fatalf("the wording model needs the step and the site, got %s", prompt)
	}
	if wording.Text != "아래 링크를 열어 거래처 포털에 직접 로그인한 뒤 돌려주세요." {
		t.Fatalf("a handoff message carries no approval details beneath it, got %q", wording.Text)
	}
}

func TestTheWordingModelWritesInTheRequestersLanguage(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		responseLanguage string
		expected         string
	}{
		{name: "english", responseLanguage: "en", expected: "in English."},
		{name: "korean", responseLanguage: "ko", expected: "in Korean."},
		{name: "japanese", responseLanguage: "ja", expected: "in Japanese."},
		{name: "unknown", responseLanguage: "", expected: "in the language the requester wrote in."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			languageModel := &wordingLanguageModel{question: "Send it?"}
			facts := hostFactsFixture()
			facts.ResponseLanguage = testCase.responseLanguage

			NewWorder(languageModel).WordQuestion(context.Background(), facts)

			prompt := languageModel.promptSeen()
			if !strings.Contains(prompt, testCase.expected) {
				t.Fatalf("expected the wording model to be told to write %q, got %s", testCase.expected, prompt)
			}
			if testCase.responseLanguage != "ko" && strings.Contains(prompt, "Korean") {
				t.Fatalf("a requester who is not Korean must not be asked in Korean, got %s", prompt)
			}
		})
	}
}

type answeringLanguageModel struct {
	questions []string
	labels    []map[string]string
	requests  []model.StructuredResponseRequest
}

func (languageModel *answeringLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("the approval gate only asks for structured output")
}

func (languageModel *answeringLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	question := languageModel.questions[len(languageModel.requests)]
	var labels map[string]string
	if len(languageModel.labels) > len(languageModel.requests) {
		labels = languageModel.labels[len(languageModel.requests)]
	}
	languageModel.requests = append(languageModel.requests, request)
	content, errorValue := answerAsking(request, question, labels)
	return model.StructuredResponse{Content: content}, errorValue
}

func TestAQuestionWrittenOverSeveralLinesIsAskedForAgainAsOne(t *testing.T) {
	languageModel := &answeringLanguageModel{questions: []string{"Delete Team sync?\n\n1. Delete now\n2. Cancel", "Delete Team sync?"}}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), factsWithoutChoices())

	if wording.Failure != nil || !strings.HasPrefix(wording.Text, "Delete Team sync?\n\n") || strings.Contains(wording.Text, "Delete now") {
		t.Fatalf("the requester reads the one asking sentence, never a list the model added, got %+v", wording)
	}
	if len(languageModel.requests) != 2 || languageModel.requests[1].Messages[len(languageModel.requests[1].Messages)-1].Content != oneLineAgain {
		t.Fatalf("the model is told once why its question was refused, got %d requests", len(languageModel.requests))
	}
}

func TestAQuestionStillOverSeveralLinesFallsBackToTheRawSummary(t *testing.T) {
	languageModel := &answeringLanguageModel{questions: []string{"Delete Team sync?\n1. Now", "Delete Team sync?\n1. Now"}}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), factsWithoutChoices())

	if !errors.Is(wording.Failure, errQuestionSpansLines) || !strings.HasPrefix(wording.Text, "calendar_event_delete Team sync\n\n") {
		t.Fatalf("a question that will not fit one line is a wording failure, reported and never shown, got %+v", wording)
	}
}

func TestAQuestionOfferingGivenChoicesMaySpanLines(t *testing.T) {
	languageModel := &answeringLanguageModel{questions: []string{"Delete Team sync?\n1. Now\n2. Tomorrow 9:00\n3. Cancel"}}

	wording := NewWorder(languageModel).WordQuestion(context.Background(), hostFactsFixture())

	if wording.Failure != nil || len(languageModel.requests) != 1 || !strings.Contains(wording.Text, "2. Tomorrow 9:00") {
		t.Fatalf("a question offering the choices it was given lists them, got %+v after %d requests", wording, len(languageModel.requests))
	}
}

func factsWithoutChoices() holdrecord.QuestionFacts {
	facts := hostFactsFixture()
	facts.Choices = nil
	return facts
}
