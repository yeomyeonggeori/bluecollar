package loop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const approvalUnmatchedObservationNote = "This is not the call that was held for approval. The held call is still waiting, and what ran here was recorded as its own effect."

func newHoldID() string {
	buffer := make([]byte, 16)
	if _, errorValue := rand.Read(buffer); errorValue != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}

func (agentTurnRunner *AgentTurnRunner) recordHold(taskRunID string, observation turnObservation) {
	hold := HeldCall{
		ApprovalToken: newHoldID(),
		ToolName:      strings.TrimSpace(observation.Tool),
		ToolInput:     observation.ToolInput,
		ObservationID: observation.ObservationID,
	}
	if hold.ApprovalToken == "" {
		return
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventApprovalHeldCall, marshalEventBody(hold))
}

func (agentTurnRunner *AgentTurnRunner) unspentHolds(taskRunID string) []HeldCall {
	holds := []HeldCall{}
	spentHoldIDs := map[string]bool{}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHeldCall:
			hold := HeldCall{}
			if json.Unmarshal([]byte(taskEvent.Body), &hold) == nil && hold.ApprovalToken != "" {
				hold.ToolName = toolcontract.CanonicalToolName(hold.ToolName)
				holds = append(holds, hold)
			}
		case agentcontract.TaskEventApprovalExecuted:
			executed := HeldCall{}
			if json.Unmarshal([]byte(taskEvent.Body), &executed) == nil {
				spentHoldIDs[executed.ApprovalToken] = true
			}
		}
	}
	awaiting := []HeldCall{}
	for _, hold := range holds {
		if !spentHoldIDs[hold.ApprovalToken] {
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
	holdID := strings.TrimSpace(carriedOutCall.ApprovalToken)
	if holdID == "" {
		return HeldCall{}, false
	}
	toolInputKey := canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput)
	for _, hold := range holds {
		if hold.ApprovalToken == holdID && hold.CanonicalCallKey() == toolInputKey {
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
		"toolName":            strings.TrimSpace(carriedOutCall.ToolName),
		"toolInputKey":        canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput),
		"presentedToken":      strings.TrimSpace(carriedOutCall.ApprovalToken),
		"awaitingHeldCallIDs": holdObservationIDs(holds),
	}))
	return true
}

// The summary is the loop's sentence about an observation, so a note belongs there.
// Output is the tool's, and a result contract that promised JSON still has to parse.
func observationNotingApprovalDrift(observation turnObservation) turnObservation {
	observation.Summary = strings.TrimSpace(observation.Summary + " " + approvalUnmatchedObservationNote)
	return observation
}

func holdObservationIDs(holds []HeldCall) []string {
	observationIDs := make([]string, 0, len(holds))
	for _, hold := range holds {
		observationIDs = append(observationIDs, hold.ObservationID)
	}
	return observationIDs
}
