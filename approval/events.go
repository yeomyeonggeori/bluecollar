package approval

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func (gate *Gate) recordHold(request approvalRequest, question string) holdrecord.Hold {
	hold := holdrecord.Open(gate.taskRuns, request.taskRunID, agentcontract.HeldCall{
		ToolName:      request.toolDefinition.Name,
		ToolInput:     request.toolInput,
		ApprovalScope: request.approvalScope(),
		Confirmation:  question,
	}, nil)
	gate.recordApprovalQuestion(request, question)
	return hold
}

func (gate *Gate) recordApprovalQuestion(request approvalRequest, question string) {
	gate.taskRuns.AppendTaskEvent(request.taskRunID, agentcontract.TaskEventConfirmationRequested, marshalEventBody(map[string]string{
		"userFacingMessage": question,
		"message":           question,
		"reasonCode":        approvalReasonCode(request),
		"reasonDetail":      "approval gate for " + request.toolDefinition.Name,
		"responseLanguage":  request.turn.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRuns.AppendTaskEvent(request.taskRunID, agentcontract.TaskEventAskRequested, marshalEventBody(askRecord(request, question)))
}

func approvalReasonCode(request approvalRequest) string {
	if sideEffectClass := strings.TrimSpace(request.toolDefinition.SideEffectClass); sideEffectClass != "" {
		return sideEffectClass
	}
	return "approval_required"
}

func askRecord(request approvalRequest, question string) map[string]any {
	record := map[string]any{
		"kind":             "ask_confirm",
		"message":          question,
		"reasonCode":       approvalReasonCode(request),
		"reasonDetail":     "approval gate for " + request.toolDefinition.Name,
		"responseLanguage": request.turn.ResponseLanguage,
	}
	if approvalScope := request.approvalScope(); approvalScope != "" {
		record["approvalScope"] = approvalScope
		record["sessionApprovable"] = true
	}
	return record
}
