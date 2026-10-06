package intake

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func TestTheOutputFormatQuestionsAreTheFormatsTheRuntimeAccepts(t *testing.T) {
	questions := questionsFor(addressedDecisionRequest("보고서 정리해줘"))
	askedFormats := []string{}
	for questionName := range questions {
		if formatName, isFormat := strippedQuestionPrefix(questionName, "m1."+agentcontract.IntakeQuestionPrefixFormat); isFormat {
			askedFormats = append(askedFormats, formatName)
			if !turnclassification.IsRequestedOutputFormatName(formatName) {
				t.Fatalf("the decision asks about %q, which the runtime does not accept as an output format", formatName)
			}
		}
	}
	if len(askedFormats) != len(turnclassification.RequestedOutputFormatNames) {
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

func requestsAskingEveryOptionalQuestion() map[string]turnclassification.IntakeDecisionRequest {
	request := addressedDecisionRequest("그거 말고 다른 걸로 해줘")
	request.ResponseLanguage = ""
	request.PriorTask = agentcontract.PriorTaskContext{TaskRunID: "task-run-prior", Prompt: "지난번 보고서"}
	return map[string]turnclassification.IntakeDecisionRequest{
		"a message that follows a prior task in an unknown language": request,
	}
}
