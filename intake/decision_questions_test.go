package intake

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func questionsFor(request agentcontract.IntakeDecisionRequest) map[string]model.DecisionQuestion {
	builderRequest := request
	builderRequest.CallableToolNames = resolveCallableToolNames(request)
	return newQuestionBuilder(builderRequest).questions()
}

func criteriaText(t *testing.T, question model.DecisionQuestion) string {
	t.Helper()
	document, errorValue := json.Marshal(question.Criteria)
	if errorValue != nil {
		t.Fatalf("expected the criteria to marshal: %v", errorValue)
	}
	return question.Instructions + " " + string(document)
}

func TestTheRouteQuestionReservesGiveUpForImpossibleWork(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("이 파일로 덱 만들어줘"))

	routeCriteria := criteriaText(t, questions["m1."+agentcontract.IntakeQuestionRoute])
	if !strings.Contains(routeCriteria, "Never for a permission concern, which the operating system decides at execution") {
		t.Fatalf("expected give_up to stay out of permission decisions, got %s", routeCriteria)
	}
	classificationCriteria := criteriaText(t, questions["m1."+agentcontract.IntakeQuestionClassification])
	if !strings.Contains(classificationCriteria, "pointless to even attempt") {
		t.Fatalf("expected unsupported to be reserved for pointless work, got %s", classificationCriteria)
	}
}

func TestEveryQuestionOptionIsAString(t *testing.T) {
	request := addressedDecisionRequest("보고서 정리해줘")
	request.ToolSet = newTestToolSet([]string{"task_add"})

	for questionName, question := range questionsFor(request) {
		switch question.Type {
		case model.DecisionQuestionTypeChoice:
			criteria, isOptionMap := question.Criteria.(map[string]string)
			if !isOptionMap || len(criteria) == 0 {
				t.Fatalf("expected %s to carry named string options, got %+v", questionName, question.Criteria)
			}
		case model.DecisionQuestionTypeNoul:
			criteria, isTwoSided := question.Criteria.(map[string]string)
			if !isTwoSided || criteria["true"] == "" || criteria["false"] == "" {
				t.Fatalf("expected %s to carry a two-sided criterion, got %+v", questionName, question.Criteria)
			}
		default:
			t.Fatalf("expected %s to be a choice or a noul, got %q", questionName, question.Type)
		}
		if strings.TrimSpace(question.Instructions) == "" {
			t.Fatalf("expected %s to carry instructions", questionName)
		}
	}
}

func TestTheEmojiAndDutyOptionsAreTheOnesTheRuntimeAccepts(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("배포 끝났습니다"))

	emojiOptions, _ := questions["m1."+agentcontract.IntakeQuestionReactionEmoji].Criteria.(map[string]string)
	for _, emojiName := range agentcontract.ReactionEmojiNames {
		if _, isOffered := emojiOptions[emojiName]; !isOffered {
			t.Fatalf("expected %s to be offered as a reaction", emojiName)
		}
	}
	if len(emojiOptions) != len(agentcontract.ReactionEmojiNames) {
		t.Fatalf("expected exactly the accepted emoji names, got %d options", len(emojiOptions))
	}

	dutyOptions, _ := questions["m1."+agentcontract.IntakeQuestionDuty].Criteria.(map[string]string)
	if _, hasNone := dutyOptions[agentcontract.IntakeDutyOptionNone]; !hasNone {
		t.Fatal("expected the duty question to offer none")
	}
	for _, duty := range agentcontract.StandingDuties() {
		if _, isOffered := dutyOptions[duty.Name]; !isOffered {
			t.Fatalf("expected the standing duty %s to be offered", duty.Name)
		}
	}
}

func TestASingleSelectPendingChoiceIsOneQuestionWithANoneOption(t *testing.T) {
	request := addressedDecisionRequest("두 번째로 해줘")
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID: "task-run-1",
		Question:  "어떤 형식으로 드릴까요?",
		Options:   []agentcontract.ChoiceReplyOption{{Key: "table", Label: "표"}, {Key: "graph", Label: "그래프"}, {Key: "table", Label: "중복"}},
	}

	questions := questionsFor(request)

	if _, isAsked := questions["m1."+agentcontract.IntakeQuestionPrefixChoice+"table"]; isAsked {
		t.Fatal("expected a single-select choice to be asked as one question, not per option")
	}
	options, isChoice := questions["m1."+agentcontract.IntakeQuestionChoice].Criteria.(map[string]string)
	if !isChoice {
		t.Fatalf("expected one choice question, got %+v", questions["m1."+agentcontract.IntakeQuestionChoice])
	}
	if len(options) != 3 {
		t.Fatalf("expected the two distinct options plus none, got %+v", options)
	}
	for _, optionName := range []string{"table", "graph", agentcontract.IntakeChoiceOptionNone} {
		if _, isOffered := options[optionName]; !isOffered {
			t.Fatalf("expected %s to be offered, got %+v", optionName, options)
		}
	}
}

func TestAMultiSelectPendingChoiceIsOneQuestionPerOption(t *testing.T) {
	request := addressedDecisionRequest("표랑 그래프 둘 다")
	request.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID:     "task-run-1",
		Question:      "어떤 형식으로 드릴까요?",
		SelectionMode: "multiple",
		Options:       []agentcontract.ChoiceReplyOption{{Key: "table", Label: "표"}, {Key: "graph", Label: "그래프"}},
	}

	questions := questionsFor(request)

	if _, isAsked := questions["m1."+agentcontract.IntakeQuestionChoice]; isAsked {
		t.Fatal("expected a multi-select choice to be asked per option")
	}
	for _, optionKey := range []string{"table", "graph"} {
		if _, isAsked := questions["m1."+agentcontract.IntakeQuestionPrefixChoice+optionKey]; !isAsked {
			t.Fatalf("expected option %s to be asked", optionKey)
		}
	}
}

func TestTheApprovalQuestionIsAskedOnlyForAPendingConfirmation(t *testing.T) {
	if _, isAsked := questionsFor(addressedDecisionRequest("응"))["m1."+agentcontract.IntakeQuestionApproval]; isAsked {
		t.Fatal("expected no approval question without a pending confirmation")
	}

	request := addressedDecisionRequest("응")
	request.PendingConfirmation = agentcontract.PendingConfirmationContext{TaskRunID: "task-run-1", Question: "삭제할까요?"}
	if _, isAsked := questionsFor(request)["m1."+agentcontract.IntakeQuestionApproval]; !isAsked {
		t.Fatal("expected the approval question for a pending confirmation")
	}
}

func TestTheStateShowsHowStaleAPendingQuestionIs(t *testing.T) {
	now := time.Date(2026, 9, 14, 15, 8, 0, 0, time.UTC)
	request := addressedDecisionRequest("응")
	request.EnvironmentNow = now
	request.PendingConfirmation = agentcontract.PendingConfirmationContext{
		TaskRunID:      "task-run-1",
		Prompt:         "15일 일정 삭제",
		Question:       "삭제할까요?",
		AskedAt:        now.Add(-2*time.Hour - 28*time.Minute),
		ExchangesSince: 3,
	}

	state := buildDecisionState(request, nil)

	if state.PendingConfirmation == nil {
		t.Fatal("expected the pending confirmation in the state")
	}
	if state.PendingConfirmation.AskedAgo != "2h28m0s ago" {
		t.Fatalf("expected the age of the pending question, got %q", state.PendingConfirmation.AskedAgo)
	}
	if state.PendingConfirmation.ExchangesSince != 3 {
		t.Fatalf("expected the exchanges since, got %d", state.PendingConfirmation.ExchangesSince)
	}
	if !strings.Contains(criteriaText(t, questionsFor(request)["m1."+agentcontract.IntakeQuestionApproval]), "exchangesSince") {
		t.Fatal("expected the approval question to read the staleness from the state")
	}
}

func TestTheStateNamesEachMessageTheQuestionsAskAbout(t *testing.T) {
	request := addressedDecisionRequest("보고서 정리해줘")
	request.Messages = append(request.Messages, agentcontract.IntakeDecisionMessage{MessageID: "message-2", Prompt: "표도 넣어줘"})

	state := buildDecisionState(request, nil)

	if len(state.Messages) != 2 || state.Messages[0].ID != "m1" || state.Messages[1].ID != "m2" {
		t.Fatalf("expected the messages to be named m1 and m2, got %+v", state.Messages)
	}
	if !strings.Contains(questionsFor(request)["m2."+agentcontract.IntakeQuestionRoute].Instructions, "message m2") {
		t.Fatal("expected each question to name the message it asks about")
	}
}
