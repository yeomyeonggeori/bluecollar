package loop

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type staticReplyProvider struct {
	content string
}

func (replyProvider staticReplyProvider) GenerateResponse(context.Context, string) (string, error) {
	return replyProvider.content, nil
}

func (replyProvider staticReplyProvider) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{Content: replyProvider.content}, nil
}
