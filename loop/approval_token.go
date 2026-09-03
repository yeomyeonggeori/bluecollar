package loop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"strings"
)

const approvalUnmatchedObservationNote = "This is not the call that was held for approval. The held call is still waiting, and what ran here was recorded as its own effect."

func newApprovalToken() string {
	buffer := make([]byte, 16)
	if _, errorValue := rand.Read(buffer); errorValue != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}

func (agentTurnRunner *AgentTurnRunner) mintHeldCallApproval(taskRunID string, observation turnObservation) {
	heldCall := HeldCall{
		ApprovalToken: newApprovalToken(),
		ToolName:      strings.TrimSpace(observation.Tool),
		ToolInput:     observation.ToolInput,
		ObservationID: observation.ObservationID,
	}
	if heldCall.ApprovalToken == "" {
		return
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventApprovalHeldCall, marshalEventBody(heldCall))
}

func (agentTurnRunner *AgentTurnRunner) heldCallsAwaitingApproval(taskRunID string) []HeldCall {
	heldCalls := []HeldCall{}
	spentTokens := map[string]bool{}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHeldCall:
			heldCall := HeldCall{}
			if json.Unmarshal([]byte(taskEvent.Body), &heldCall) == nil && heldCall.ApprovalToken != "" {
				heldCalls = append(heldCalls, heldCall)
			}
		case agentcontract.TaskEventApprovalExecuted:
			executed := HeldCall{}
			if json.Unmarshal([]byte(taskEvent.Body), &executed) == nil {
				spentTokens[executed.ApprovalToken] = true
			}
		}
	}
	awaiting := []HeldCall{}
	for _, heldCall := range heldCalls {
		if !spentTokens[heldCall.ApprovalToken] {
			awaiting = append(awaiting, heldCall)
		}
	}
	return awaiting
}

func toolWasHeldForApproval(heldCalls []HeldCall, toolName string) bool {
	trimmedToolName := strings.TrimSpace(toolName)
	for _, heldCall := range heldCalls {
		if heldCall.ToolName == trimmedToolName {
			return true
		}
	}
	return false
}

func heldCallForCarriedOutCall(heldCalls []HeldCall, carriedOutCall CarriedOutCall) (HeldCall, bool) {
	approvalToken := strings.TrimSpace(carriedOutCall.ApprovalToken)
	if approvalToken == "" {
		return HeldCall{}, false
	}
	toolInputKey := canonicalToolCallKey(carriedOutCall.ToolName, carriedOutCall.ToolInput)
	for _, heldCall := range heldCalls {
		if heldCall.ApprovalToken == approvalToken && heldCall.CanonicalCallKey() == toolInputKey {
			return heldCall, true
		}
	}
	return HeldCall{}, false
}

func (agentTurnRunner *AgentTurnRunner) noteDriftFromHeldCall(taskRunID string, heldCalls []HeldCall, carriedOutCall CarriedOutCall) (didDriftFromItsHold bool) {
	if !toolWasHeldForApproval(heldCalls, carriedOutCall.ToolName) {
		return false
	}
	if _, isMatched := heldCallForCarriedOutCall(heldCalls, carriedOutCall); isMatched {
		return false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventApprovalUnheldCallCarriedOut, marshalEventBody(map[string]any{
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

func heldCallObservationIDs(heldCalls []HeldCall) []string {
	observationIDs := make([]string, 0, len(heldCalls))
	for _, heldCall := range heldCalls {
		observationIDs = append(observationIDs, heldCall.ObservationID)
	}
	return observationIDs
}
