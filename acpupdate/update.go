package acpupdate

import (
	"encoding/json"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func ForEvent(rawTurnEvent taskstate.RawTurnEvent) acp.SessionUpdate {
	if toolCallUpdate, isToolCall := ToolCallForEvent(rawTurnEvent); isToolCall {
		return toolCallUpdate
	}
	return acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
		Content: acp.TextBlock(rawTurnEvent.Name),
		Meta:    ledgerMeta(rawTurnEvent),
	}}
}

func ToolCallForEvent(rawTurnEvent taskstate.RawTurnEvent) (acp.SessionUpdate, bool) {
	meta := ledgerMeta(rawTurnEvent)
	if toolName, isRequest := agentcontract.ToolTaskEventToolName(rawTurnEvent.Name, agentcontract.ToolTaskEventRequestedSuffix); isRequest {
		return acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: acp.ToolCallId(observationIDOfEvent(rawTurnEvent.Body)),
			Title:      ToolCallTitle(toolName, rawTurnEvent.Body),
			Status:     acp.ToolCallStatusPending,
			RawInput:   rawInputOfEvent(rawTurnEvent.Body),
			Meta:       meta,
		}}, true
	}
	if _, isResult := agentcontract.ToolTaskEventToolName(rawTurnEvent.Name, agentcontract.ToolTaskEventResultSuffix); isResult {
		status := acp.ToolCallStatusCompleted
		if isFailureEvent(rawTurnEvent.Body) {
			status = acp.ToolCallStatusFailed
		}
		return acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: acp.ToolCallId(observationIDOfEvent(rawTurnEvent.Body)),
			Status:     &status,
			RawOutput:  json.RawMessage(rawTurnEvent.Body),
			Meta:       meta,
		}}, true
	}
	return acp.SessionUpdate{}, false
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
