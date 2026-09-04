package toolcontract

import "context"

type ToolCallReview struct {
	MayProceed bool
	Result     ToolResult
	// Names the held call this approval spends, for a tool whose backend has to
	// be told which approval it is running under.
	ApprovedCallID string
}

type ToolCallGate interface {
	ReviewToolCall(context.Context, ToolInvocation, ToolDefinition) (ToolCallReview, error)
}

func (toolSet *ToolSet) UseToolCallGate(toolCallGate ToolCallGate) {
	if toolSet == nil {
		return
	}
	toolSet.toolCallGate = toolCallGate
}

func (toolSet *ToolSet) reviewToolCall(ctx context.Context, toolInvocation ToolInvocation, toolDefinition ToolDefinition) (context.Context, ToolResult, bool) {
	if toolSet.toolCallGate == nil {
		return ctx, ToolResult{}, false
	}
	review, errorValue := toolSet.toolCallGate.ReviewToolCall(ctx, toolInvocation, toolDefinition)
	if errorValue != nil {
		return ctx, ToolFailureResult(FailureUnknown, FailureCodes.OperationFailed, "tool_call_gate", errorValue.Error()), true
	}
	if review.MayProceed {
		return WithApprovedCallID(ctx, review.ApprovedCallID), ToolResult{}, false
	}
	return ctx, review.Result, true
}
