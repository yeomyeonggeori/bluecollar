package approval

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
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
		return toolcontract.ToolCallReview{Result: approvalcore.DelegatedTurn()}, nil
	}
	return reviewForOutcome(turnGate.gate.awaitApproval(ctx, approvalRequest{
		turn:           turnGate.turn,
		taskRunID:      strings.TrimSpace(toolcontract.TaskRunIDFromContext(ctx)),
		toolDefinition: toolDefinition,
		toolInput:      toolInvocation.Input,
	})), nil
}

func reviewForOutcome(decided approvalcore.Outcome) toolcontract.ToolCallReview {
	switch decided.Verdict {
	case approvalcore.Approved:
		return toolcontract.ToolCallReview{MayProceed: true, HoldID: decided.HoldID}
	case approvalcore.Rejected:
		return toolcontract.ToolCallReview{Result: approvalcore.Declined()}
	case approvalcore.Unanswered:
		return toolcontract.ToolCallReview{Result: unansweredCallResult()}
	}
	return toolcontract.ToolCallReview{Result: unanswerableCallResult()}
}

func unansweredCallResult() toolcontract.ToolResult {
	return approvalcore.Refusal("The requester did not answer the approval question, so this call did not run. Do not retry it now; ask again later if it is still needed, or take another route.")
}

func unanswerableCallResult() toolcontract.ToolResult {
	return approvalcore.Refusal("This call needs the requester's approval and there is no one to ask, so it cannot run. Do not wait for an approval; take another route or tell them what you could not do.")
}
