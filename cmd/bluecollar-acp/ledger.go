package main

import (
	"context"
	"encoding/json"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type sessionUpdateSender interface {
	SessionUpdate(context.Context, acp.SessionNotification) error
}

func sendLedgerEvent(ctx context.Context, sender sessionUpdateSender, sessionID acp.SessionId, rawTurnEvent taskstate.RawTurnEvent) {
	if sender == nil {
		return
	}
	sender.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sessionID,
		Update:    sessionUpdateForEvent(rawTurnEvent),
	})
}

func sessionUpdateForEvent(rawTurnEvent taskstate.RawTurnEvent) acp.SessionUpdate {
	meta := ledgerMeta(rawTurnEvent)
	if toolName, isRequest := taskstate.ToolTaskEventToolName(rawTurnEvent.Name, ".requested"); isRequest {
		return acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: acp.ToolCallId(observationIDOfEvent(rawTurnEvent.Body)),
			Title:      toolName,
			Status:     acp.ToolCallStatusPending,
			RawInput:   rawInputOfEvent(rawTurnEvent.Body),
			Meta:       meta,
		}}
	}
	if _, isResult := taskstate.ToolTaskEventToolName(rawTurnEvent.Name, ".result"); isResult {
		status := acp.ToolCallStatusCompleted
		if isFailureEvent(rawTurnEvent.Body) {
			status = acp.ToolCallStatusFailed
		}
		return acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: acp.ToolCallId(observationIDOfEvent(rawTurnEvent.Body)),
			Status:     &status,
			RawOutput:  json.RawMessage(rawTurnEvent.Body),
			Meta:       meta,
		}}
	}
	return acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
		Content: acp.TextBlock(rawTurnEvent.Name),
		Meta:    meta,
	}}
}

func ledgerMeta(rawTurnEvent taskstate.RawTurnEvent) map[string]any {
	record := agentcontract.LedgerRecord{Name: rawTurnEvent.Name}
	if json.Valid([]byte(rawTurnEvent.Body)) {
		record.Body = json.RawMessage(rawTurnEvent.Body)
	} else {
		quoted, _ := json.Marshal(rawTurnEvent.Body)
		record.Body = quoted
	}
	return map[string]any{agentcontract.LedgerMetaKey: record}
}

func observationIDOfEvent(body string) string {
	decoded := struct {
		ObservationID string `json:"observationID"`
	}{}
	json.Unmarshal([]byte(body), &decoded)
	return decoded.ObservationID
}

func rawInputOfEvent(body string) any {
	decoded := struct {
		Input json.RawMessage `json:"input"`
	}{}
	if json.Unmarshal([]byte(body), &decoded) != nil || len(decoded.Input) == 0 {
		return nil
	}
	return decoded.Input
}

func isFailureEvent(body string) bool {
	decoded := struct {
		Failure *json.RawMessage `json:"failure"`
	}{}
	return json.Unmarshal([]byte(body), &decoded) == nil && decoded.Failure != nil
}

func replayLedger(openSession *session, promptMeta map[string]any) bool {
	records, isPresent := ledgerRecordsOfMeta(promptMeta)
	if !isPresent || len(records) == 0 {
		return false
	}
	taskRun := openSession.taskRuns.CreateTaskRunWithOrigin(requesterPersonID, taskstate.TaskRunOrigin{}, "")
	openSession.taskRuns.AdvanceTaskRun(taskRun.TaskRunID, "")
	for _, record := range records {
		openSession.taskRuns.AppendTaskEvent(taskRun.TaskRunID, record.Name, string(record.Body))
	}
	openSession.rememberTaskRun(taskRun.TaskRunID)
	return true
}

func ledgerRecordsOfMeta(promptMeta map[string]any) ([]agentcontract.LedgerRecord, bool) {
	value, isPresent := promptMeta[agentcontract.LedgerMetaKey]
	if !isPresent {
		return nil, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return nil, false
	}
	records := []agentcontract.LedgerRecord{}
	if json.Unmarshal(encoded, &records) != nil {
		return nil, false
	}
	return records, true
}

func carriedOutCallsOfMeta(promptMeta map[string]any) []agentcontract.CarriedOutCall {
	value, isPresent := promptMeta[agentcontract.CarriedOutCallMetaKey]
	if !isPresent {
		return nil
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return nil
	}
	carriedOutCalls := []agentcontract.CarriedOutCall{}
	if json.Unmarshal(encoded, &carriedOutCalls) != nil {
		return nil
	}
	return carriedOutCalls
}
