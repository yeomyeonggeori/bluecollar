package intake

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type describingLanguageModel struct {
	request model.StructuredResponseRequest
}

func (languageModel *describingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *describingLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.request = request
	return model.StructuredResponse{Content: `{"descriptions":["화이트보드 사진"]}`}, nil
}

func TestAnAttachmentDescriberSendsTheImageAndReadsBackItsDescription(t *testing.T) {
	languageModel := &describingLanguageModel{}
	parts := []agentcontract.AgentPart{{Type: agentcontract.AgentPartTypeImage, Image: &agentcontract.AgentImagePart{MimeType: "image/png", DataBase64: "aGVsbG8="}}}

	descriptions, errorValue := NewAttachmentDescriber(languageModel).DescribeAttachments(context.Background(), parts)

	if errorValue != nil || len(descriptions) != 1 || descriptions[0] != "화이트보드 사진" {
		t.Fatalf("expected the description the model wrote, got %v %v", descriptions, errorValue)
	}
	userMessage := languageModel.request.Messages[len(languageModel.request.Messages)-1]
	if len(userMessage.Parts) != 1 || userMessage.Parts[0].DataBase64 != "aGVsbG8=" {
		t.Fatalf("expected the picture itself in the call, got %+v", userMessage.Parts)
	}
}

func TestWithoutALanguageModelThereIsNoAttachmentDescriber(t *testing.T) {
	if NewAttachmentDescriber(nil) != nil {
		t.Fatal("a describer with no model to ask would only fail, so none is built")
	}
}
