package acpagent

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type hostCheckedGate struct {
	inner toolcontract.ToolCallGate
}

func newHostCheckedGate(inner toolcontract.ToolCallGate) toolcontract.ToolCallGate {
	return hostCheckedGate{inner: inner}
}

func (gate hostCheckedGate) ReviewToolCall(ctx context.Context, invocation toolcontract.ToolInvocation, definition toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	if definition.IsHostGated {
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	}
	return gate.inner.ReviewToolCall(ctx, invocation, definition)
}
