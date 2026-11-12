package loop

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func ContextAskedByTheRequester(ctx context.Context, request AgentTurnRequest) context.Context {
	return model.ContextWithRequestContext(ctx, model.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	})
}
