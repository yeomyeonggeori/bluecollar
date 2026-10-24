package loop

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

type toolRequestEventKey struct{}

func WithToolRequestEvent(ctx context.Context, requestEvent taskstate.RawTurnEvent) context.Context {
	return context.WithValue(ctx, toolRequestEventKey{}, requestEvent)
}

func ToolRequestEventFromContext(ctx context.Context) (taskstate.RawTurnEvent, bool) {
	requestEvent, isPresent := ctx.Value(toolRequestEventKey{}).(taskstate.RawTurnEvent)
	return requestEvent, isPresent
}
