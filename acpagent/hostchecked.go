package acpagent

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type hostCheckedGate struct {
	inner                toolcontract.ToolCallGate
	hostCheckedToolNames map[string]bool
}

func newHostCheckedGate(inner toolcontract.ToolCallGate, hostCheckedToolNames []string) toolcontract.ToolCallGate {
	if len(hostCheckedToolNames) == 0 {
		return inner
	}
	toolNames := map[string]bool{}
	for _, toolName := range hostCheckedToolNames {
		toolNames[toolName] = true
	}
	return hostCheckedGate{inner: inner, hostCheckedToolNames: toolNames}
}

func (gate hostCheckedGate) ReviewToolCall(ctx context.Context, invocation toolcontract.ToolInvocation, definition toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	if gate.hostCheckedToolNames[definition.Name] {
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	}
	return gate.inner.ReviewToolCall(ctx, invocation, definition)
}
