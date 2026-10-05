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

func (gate *Gate) recordHold(request approvalRequest, question string) Hold {
	call := agentcontract.HeldCall{
		ApprovalToken: taskstate.NewIdentifier(),
		ToolName:      request.toolDefinition.Name,
		ToolInput:     request.toolInput,
		ApprovalScope: request.approvalScope(),
		Confirmation:  question,
	}
	gate.taskRuns.AppendTaskEvent(request.taskRunID, agentcontract.TaskEventApprovalPendingCall, marshalEventBody(call))
	gate.recordApprovalQuestion(request, question)
	return Hold{ID: call.ApprovalToken, Call: call, taskRunID: request.taskRunID, state: holdPending}
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

func recordDecision(taskRunStore taskstate.TaskRunStore, hold Hold, decision string, source string) {
	taskRunStore.AppendTaskEvent(hold.taskRunID, agentcontract.TaskEventApprovalDecided, marshalEventBody(decidedBody{
		HoldID:   hold.ID,
		Decision: decision,
		Source:   source,
	}))
}

func recordSpent(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, toolName string, toolInput json.RawMessage) {
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalExecuted, marshalEventBody(spentBody{
		HoldID:    holdID,
		ToolName:  strings.TrimSpace(toolName),
		ToolInput: toolInput,
	}))
}

func grantApprovalScope(taskRunStore taskstate.TaskRunStore, hold Hold) {
	approvalScope := strings.TrimSpace(hold.Call.ApprovalScope)
	if approvalScope == "" {
		return
	}
	taskRunStore.AppendTaskEvent(hold.taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshalEventBody(scopeGrantedBody{Scope: approvalScope}))
}
