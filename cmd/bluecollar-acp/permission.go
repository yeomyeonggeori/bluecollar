package main

import (
	"context"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/approval"
)

const (
	approveOptionID acp.PermissionOptionId = "allow"
	rejectOptionID  acp.PermissionOptionId = "reject"
)

type permissionAsker struct {
	requester permissionRequester
	sessionID acp.SessionId
}

func (asker permissionAsker) Ask(ctx context.Context, hold approval.Hold) approval.Answer {
	response, errorValue := asker.requester.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: asker.sessionID,
		ToolCall:  permissionToolCall(hold),
		Options: []acp.PermissionOption{
			{OptionId: approveOptionID, Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: rejectOptionID, Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
		},
	})
	if errorValue != nil || response.Outcome.Selected == nil {
		return approval.NoAnswer
	}
	return answerForOption(response.Outcome.Selected.OptionId)
}

func permissionToolCall(hold approval.Hold) acp.ToolCallUpdate {
	title := hold.Call.Confirmation
	return acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(hold.ID), Title: &title, RawInput: hold.Call.ToolInput}
}

func answerForOption(optionID acp.PermissionOptionId) approval.Answer {
	switch optionID {
	case approveOptionID:
		return approval.Approved
	case rejectOptionID:
		return approval.Rejected
	}
	return approval.NoAnswer
}
