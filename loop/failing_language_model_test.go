package loop

import (
	"context"
	"errors"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type failingLanguageModel struct{}

func (failingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("language model unavailable")
}

func (failingLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, errors.New("language model unavailable")
}
