package acpagent

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const (
	approveOptionID acp.PermissionOptionId = "allow"
	rejectOptionID  acp.PermissionOptionId = "reject"
)

type permissionAsker struct {
	requester permissionRequester
	sessionID acp.SessionId
}

func (asker permissionAsker) Ask(ctx context.Context, hold holdrecord.Hold) approvalcore.Verdict {
	response, errorValue := asker.requester.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: asker.sessionID,
		ToolCall:  permissionToolCall(hold),
		Options: []acp.PermissionOption{
			{OptionId: approveOptionID, Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: rejectOptionID, Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if errorValue != nil || response.Outcome.Selected == nil {
		return approvalcore.Unanswered
	}
	return answerForOption(response.Outcome.Selected.OptionId)
}

func permissionToolCall(hold holdrecord.Hold) acp.ToolCallUpdate {
	title := hold.Call.Confirmation
	return acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(hold.ID), Title: &title, RawInput: hold.Call.ToolInput}
}

func answerForOption(optionID acp.PermissionOptionId) approvalcore.Verdict {
	switch optionID {
	case approveOptionID:
		return approvalcore.Approved
	case rejectOptionID:
		return approvalcore.Rejected
	}
	return approvalcore.Unanswered
}
