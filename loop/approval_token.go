package loop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const (
	approvalHeldCallEventName        = "approval.held_call"
	approvalExecutedEventName        = "approval.executed"
	approvalUnheldCallEventName      = "approval.unheld_call_carried_out"
	approvalUnmatchedObservationNote = "This is not the call that was held for approval. The held call is still waiting, and what ran here was recorded as its own effect."
)

type heldCallRecord struct {
	ApprovalToken string `json:"approvalToken"`
	ToolName      string `json:"toolName"`
	ToolInputKey  string `json:"toolInputKey"`
	ObservationID string `json:"observationID"`
}

func newApprovalToken() string {
	buffer := make([]byte, 16)
	if _, errorValue := rand.Read(buffer); errorValue != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}

func (agentTurnRunner *AgentTurnRunner) mintHeldCallApproval(taskRunID string, observation turnObservation) {
	heldCall := heldCallRecord{
		ApprovalToken: newApprovalToken(),
		ToolName:      strings.TrimSpace(observation.Tool),
		ToolInputKey:  canonicalToolCallKey(observation.Tool, observation.ToolInput),
		ObservationID: observation.ObservationID,
	}
	if heldCall.ApprovalToken == "" {
		return
	}
	agentTurnRunner.appendEvent(taskRunID, approvalHeldCallEventName, marshalEventBody(heldCall))
}

func (agentTurnRunner *AgentTurnRunner) heldCallsAwaitingApproval(taskRunID string) []heldCallRecord {
	heldCalls := []heldCallRecord{}
	spentTokens := map[string]bool{}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		switch taskEvent.Name {
		case approvalHeldCallEventName:
			heldCall := heldCallRecord{}
			if json.Unmarshal([]byte(taskEvent.Body), &heldCall) == nil && heldCall.ApprovalToken != "" {
				heldCalls = append(heldCalls, heldCall)
			}
		case approvalExecutedEventName:
			executed := heldCallRecord{}
			if json.Unmarshal([]byte(taskEvent.Body), &executed) == nil {
				spentTokens[executed.ApprovalToken] = true
			}
		}
	}
	awaiting := []heldCallRecord{}
	for _, heldCall := range heldCalls {
		if !spentTokens[heldCall.ApprovalToken] {
			awaiting = append(awaiting, heldCall)
		}
	}
	return awaiting
}

func toolWasHeldForApproval(heldCalls []heldCallRecord, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	for _, heldCall := range heldCalls {
		if heldCall.ToolName == trimmedToolName {
			return true
		}
	}
	return false
}

func heldCallForCarriedOutCall(heldCalls []heldCallRecord, carriedOutCall CarriedOutCall) (heldCallRecord, bool) {
	approvalToken := strings.TrimSpace(carriedOutCall.ApprovalToken)
	if approvalToken == "" {
		return heldCallRecord{}, false
	}
	toolInputKey := canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput)
	for _, heldCall := range heldCalls {
		if heldCall.ApprovalToken == approvalToken && heldCall.ToolInputKey == toolInputKey {
			return heldCall, true
		}
	}
	return heldCallRecord{}, false
}

// The effect already happened, so the tool's own result is left exactly as it is.
// What a mismatch changes is the bookkeeping around it: the hold stays unspent and
// the ledger says so.
func (agentTurnRunner *AgentTurnRunner) settleHeldCallApproval(taskRunID string, heldCalls []heldCallRecord, carriedOutCall CarriedOutCall) (didDriftFromItsHold bool) {
	if !toolWasHeldForApproval(heldCalls, carriedOutCall.ToolName) {
		return false
	}
	heldCall, isMatched := heldCallForCarriedOutCall(heldCalls, carriedOutCall)
	if isMatched {
		agentTurnRunner.appendEvent(taskRunID, approvalExecutedEventName, marshalEventBody(heldCall))
		return false
	}
	agentTurnRunner.appendEvent(taskRunID, approvalUnheldCallEventName, marshalEventBody(map[string]any{
		"toolName":            strings.TrimSpace(carriedOutCall.ToolName),
		"toolInputKey":        canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput),
		"presentedToken":      strings.TrimSpace(carriedOutCall.ApprovalToken),
		"awaitingHeldCallIDs": heldCallObservationIDs(heldCalls),
	}))
	return true
}

// The summary is the loop's sentence about an observation, so a note belongs there.
// Output is the tool's, and a result contract that promised JSON still has to parse.
func observationNotingApprovalDrift(observation turnObservation) turnObservation {
	observation.Summary = strings.TrimSpace(observation.Summary + " " + approvalUnmatchedObservationNote)
	return observation
}

func heldCallObservationIDs(heldCalls []heldCallRecord) []string {
	observationIDs := make([]string, 0, len(heldCalls))
	for _, heldCall := range heldCalls {
		observationIDs = append(observationIDs, heldCall.ObservationID)
	}
	return observationIDs
}
