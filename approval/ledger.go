package approval

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func (gate *Gate) recordHold(request approvalRequest, text string) Hold {
	call := agentcontract.HeldCall{
		ApprovalToken: taskstate.NewIdentifier(),
		ToolName:      request.tool.Name,
		ToolInput:     request.input,
		ApprovalScope: request.approvalScope(),
		Confirmation:  text,
	}
	gate.taskRuns.AppendTaskEvent(request.taskRunID, agentcontract.TaskEventApprovalPendingCall, marshalEventBody(call))
	gate.recordQuestion(request, text)
	return Hold{ID: call.ApprovalToken, Call: call, taskRunID: request.taskRunID, state: holdPending}
}

func (gate *Gate) recordQuestion(request approvalRequest, text string) {
	gate.taskRuns.AppendTaskEvent(request.taskRunID, agentcontract.TaskEventConfirmationRequested, marshalEventBody(map[string]string{
		"userFacingMessage": text,
		"message":           text,
		"reasonCode":        approvalReasonCode(request),
		"reasonDetail":      "approval gate for " + request.tool.Name,
		"responseLanguage":  request.turn.ResponseLanguage,
		"source":            "tool_catalog",
	}))
	gate.taskRuns.AppendTaskEvent(request.taskRunID, agentcontract.TaskEventAskRequested, marshalEventBody(askRecord(request, text)))
}

func approvalReasonCode(request approvalRequest) string {
	if sideEffectClass := strings.TrimSpace(request.tool.SideEffectClass); sideEffectClass != "" {
		return sideEffectClass
	}
	return "approval_required"
}

func askRecord(request approvalRequest, text string) map[string]any {
	record := map[string]any{
		"kind":             "ask_confirm",
		"message":          text,
		"reasonCode":       approvalReasonCode(request),
		"reasonDetail":     "approval gate for " + request.tool.Name,
		"responseLanguage": request.turn.ResponseLanguage,
	}
	if approvalScope := request.approvalScope(); approvalScope != "" {
		record["approvalScope"] = approvalScope
		record["sessionApprovable"] = true
	}
	return record
}

func recordDecision(taskRunStore taskstate.TaskRunStore, hold Hold, decision string, source string) {
	taskRunStore.AppendTaskEvent(hold.taskRunID, agentcontract.TaskEventApprovalDecided, marshalEventBody(decidedBody{
		ApprovalToken: hold.ID,
		Decision:      decision,
		Source:        source,
	}))
}

func recordSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, toolName string, toolInput json.RawMessage) {
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalExecuted, marshalEventBody(spentBody{
		ApprovalToken: holdID,
		ToolName:      strings.TrimSpace(toolName),
		ToolInput:     toolInput,
	}))
}

func grantScope(taskRunStore taskstate.TaskRunStore, hold Hold) {
	approvalScope := strings.TrimSpace(hold.Call.ApprovalScope)
	if approvalScope == "" {
		return
	}
	taskRunStore.AppendTaskEvent(hold.taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshalEventBody(scopeGrantedBody{Scope: approvalScope}))
}
