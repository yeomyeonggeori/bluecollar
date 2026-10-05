package approval

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type turnToolCallGate struct {
	gate *Gate
	turn Turn
}

func (gate *Gate) TurnGate(turn Turn) toolcontract.ToolCallGate {
	return turnToolCallGate{gate: gate, turn: turn}
}

func (turnGate turnToolCallGate) ReviewToolCall(ctx context.Context, toolInvocation toolcontract.ToolInvocation, toolDefinition toolcontract.ToolDefinition) (toolcontract.ToolCallReview, error) {
	if !toolDefinition.RequiresApproval {
		return toolcontract.ToolCallReview{MayProceed: true}, nil
	}
	if toolcontract.IsDelegatedTurn(ctx) {
		return toolcontract.ToolCallReview{Result: delegatedTurnDeniedResult()}, nil
	}
	return reviewForRuling(turnGate.gate.awaitApproval(ctx, approvalRequest{
		turn:      turnGate.turn,
		taskRunID: strings.TrimSpace(toolcontract.TaskRunIDFromContext(ctx)),
		tool:      toolDefinition,
		input:     toolInvocation.Input,
	})), nil
}

func reviewForRuling(decided ruling) toolcontract.ToolCallReview {
	switch decided.verdict {
	case verdictApproved:
		return toolcontract.ToolCallReview{MayProceed: true, ApprovedCallID: decided.approvedCallID}
	case verdictRejected:
		return toolcontract.ToolCallReview{Result: rejectedCallResult()}
	case verdictUnanswered:
		return toolcontract.ToolCallReview{Result: unansweredCallResult()}
	}
	return toolcontract.ToolCallReview{Result: unanswerableCallResult()}
}

func unansweredCallResult() toolcontract.ToolResult {
	return refusedCallResult("The requester did not answer the approval question, so this call did not run. Do not retry it now; ask again later if it is still needed, or take another route.")
}

func unanswerableCallResult() toolcontract.ToolResult {
	return refusedCallResult("This call needs the requester's approval and there is no one to ask, so it cannot run. Do not wait for an approval; take another route or tell them what you could not do.")
}

func delegatedTurnDeniedResult() toolcontract.ToolResult {
	return refusedCallResult("This call needs the requester's approval, and a delegated turn has no one to ask: only the turn that was asked for the work can hold a call for approval. Do not wait for an approval; take another route, or report this back as the part you could not do.")
}

func rejectedCallResult() toolcontract.ToolResult {
	return refusedCallResult("The requester declined this call. Do not retry it; choose another way or stop.")
}

func refusedCallResult(notice string) toolcontract.ToolResult {
	return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.PolicyBlocked, "approval", notice)
}
