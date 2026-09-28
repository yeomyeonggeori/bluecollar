package intake

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func TestTheOutputFormatQuestionsAreTheFormatsTheRuntimeAccepts(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("보고서 정리해줘"))
	askedFormats := []string{}
	for questionName := range questions {
		if formatName, isFormat := strippedQuestionPrefix(questionName, "m1."+agentcontract.IntakeQuestionPrefixFormat); isFormat {
			askedFormats = append(askedFormats, formatName)
			if !agentcontract.IsRequestedOutputFormatName(formatName) {
				t.Fatalf("the decision asks about %q, which the runtime does not accept as an output format", formatName)
			}
		}
	}
	if len(askedFormats) != len(agentcontract.RequestedOutputFormatNames) {
		t.Fatalf("expected one question per accepted output format, got %v", askedFormats)
	}
}

func strippedQuestionPrefix(value string, prefix string) (string, bool) {
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return "", false
	}
	return value[len(prefix):], true
}

func TestTheScriptedDecisionModelKnowsEveryQuestionIntakeAsks(t *testing.T) {
	for requestName, request := range requestsAskingEveryOptionalQuestion() {
		questions := questionsFor(request)
		for questionName, question := range newQuestionBuilder(request).toolQuestions([]string{decisionMessageKey(0)}, []string{"task_add"}) {
			questions[questionName] = question
		}
		questions[singleToolChoiceQuestionName(decisionMessageKey(0))] = model.DecisionQuestion{Type: model.DecisionQuestionTypeChoice}
		_, unknownQuestionNames := intaketest.AnswersAndUnknownQuestions(questions, func(string) intaketest.Outcome { return intaketest.Outcome{} })
		if len(unknownQuestionNames) > 0 {
			t.Fatalf("%s: intake asks %v, which intaketest answers only with an empty default", requestName, unknownQuestionNames)
		}
	}
}

func requestsAskingEveryOptionalQuestion() map[string]agentcontract.IntakeDecisionRequest {
	busyRequest := addressedDecisionRequest("그거 말고 다른 걸로 해줘")
	busyRequest.ResponseLanguage = ""
	busyRequest.ActiveTask = agentcontract.ActiveTaskContext{TaskRunID: "task-run-active", Prompt: "보고서 정리해줘"}
	busyRequest.PendingConfirmation = agentcontract.PendingConfirmationContext{TaskRunID: "task-run-held", Question: "보낼까요?"}
	busyRequest.PriorTask = agentcontract.PriorTaskContext{TaskRunID: "task-run-prior", Prompt: "지난번 보고서"}
	busyRequest.PendingChoice = agentcontract.PendingChoiceContext{TaskRunID: "task-run-choice", Options: []agentcontract.ChoiceReplyOption{{Key: "first", Label: "첫째"}}}
	severalChoiceRequest := addressedDecisionRequest("둘 다")
	severalChoiceRequest.PendingChoice = agentcontract.PendingChoiceContext{
		TaskRunID:     "task-run-choice",
		SelectionMode: "multiple",
		Options:       []agentcontract.ChoiceReplyOption{{Key: "first", Label: "첫째"}, {Key: "second", Label: "둘째"}},
	}
	return map[string]agentcontract.IntakeDecisionRequest{
		"a message while work is held and running": busyRequest,
		"a message answering a multiple choice":    severalChoiceRequest,
	}
}
