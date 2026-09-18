package intaketest

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type recordingLanguageModel struct {
	request model.StructuredResponseRequest
}

func (languageModel *recordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *recordingLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.request = request
	return model.StructuredResponse{Content: `{"route":"answer_question","classification":"quick_reply","taskShape":"immediate_reply","level":"low","responseLanguage":"ko"}`}, nil
}

func TestTheLanguageModelStandInAsksARealStructuredQuestion(t *testing.T) {
	languageModel := &recordingLanguageModel{}
	decisionModel := LanguageModelDecisionModel{LanguageModel: languageModel}

	_, errorValue := decisionModel.Decide(context.Background(), model.DecisionRequest{
		State:     map[string]any{"newestMessage": map[string]string{"text": "안녕"}},
		Questions: map[string]model.DecisionQuestion{"m1.route": {Type: model.DecisionQuestionTypeChoice}},
	})
	if errorValue != nil {
		t.Fatal(errorValue)
	}

	request := languageModel.request
	if request.StructuredOutputSchema.Name != agentcontract.TurnRouterSchemaName || !strings.Contains(request.StructuredOutputSchema.Document, `"enum":["consume"`) {
		t.Fatalf("expected the router schema with the route options, got %+v", request.StructuredOutputSchema)
	}
	if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, "안녕") {
		t.Fatalf("expected the decision state to reach the model as a message, got %+v", request.Messages)
	}
}
