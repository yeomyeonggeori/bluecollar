package loop

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const approvalUnmatchedObservationNote = "This is not the call that was held for approval. The held call is still waiting, and what ran here was recorded as its own effect."

func (agentTurnRunner *AgentTurnRunner) unspentHolds(taskRunID string) []HeldCall {
	holds := []HeldCall{}
	spentHoldIDs := map[string]bool{}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHoldOpened:
			hold := HeldCall{}
			if json.Unmarshal([]byte(taskEvent.Body), &hold) == nil && hold.HoldID != "" {
				hold.ToolName = toolcontract.CanonicalToolName(hold.ToolName)
				holds = append(holds, hold)
			}
		case agentcontract.TaskEventApprovalHoldSpent:
			spent := HeldCall{}
			if json.Unmarshal([]byte(taskEvent.Body), &spent) == nil {
				spentHoldIDs[spent.HoldID] = true
			}
		}
	}
	awaiting := []HeldCall{}
	for _, hold := range holds {
		if !spentHoldIDs[hold.HoldID] {
			awaiting = append(awaiting, hold)
		}
	}
	return awaiting
}

func isToolHeld(holds []HeldCall, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	for _, hold := range holds {
		if hold.ToolName == trimmedToolName {
			return true
		}
	}
	return false
}

func holdForCarriedOutCall(holds []HeldCall, carriedOutCall CarriedOutCall) (HeldCall, bool) {
	holdID := strings.TrimSpace(carriedOutCall.HoldID)
	if holdID == "" {
		return HeldCall{}, false
	}
	toolInputKey := canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput)
	for _, hold := range holds {
		if hold.HoldID == holdID && hold.CanonicalCallKey() == toolInputKey {
			return hold, true
		}
	}
	return HeldCall{}, false
}

func (agentTurnRunner *AgentTurnRunner) noteDriftFromHold(taskRunID string, holds []HeldCall, carriedOutCall CarriedOutCall) (didDriftFromItsHold bool) {
	if !isToolHeld(holds, carriedOutCall.ToolName) {
		return false
	}
	if _, isMatched := holdForCarriedOutCall(holds, carriedOutCall); isMatched {
		return false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventApprovalUnheldCallCarriedOut, marshalEventBody(map[string]any{
		"toolName":        strings.TrimSpace(carriedOutCall.ToolName),
		"toolInputKey":    canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput),
		"presentedHoldID": strings.TrimSpace(carriedOutCall.HoldID),
		"awaitingHoldIDs": holdIDs(holds),
	}))
	return true
}

func observationNotingApprovalDrift(observation turnObservation) turnObservation {
	observation.Summary = strings.TrimSpace(observation.Summary + " " + approvalUnmatchedObservationNote)
	return observation
}

func holdIDs(holds []HeldCall) []string {
	ids := make([]string, 0, len(holds))
	for _, hold := range holds {
		ids = append(ids, hold.HoldID)
	}
	return ids
}
