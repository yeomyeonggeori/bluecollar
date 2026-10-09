package loop

import (
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

func withApprovedCallsNotYetRun(observations []turnObservation, events []agentcontract.TaskEvent) []turnObservation {
	for _, held := range holdrecord.Holds(events) {
		toolName := strings.TrimSpace(held.Call.ToolName)
		if held.State != holdrecord.StateApproved || toolName == "" {
			continue
		}
		observations = append(observations, approvedCallObservation(nextObservationIDForObservations(observations), toolName, held.Call))
	}
	return observations
}

func approvedCallObservation(observationID string, toolName string, call agentcontract.HeldCall) turnObservation {
	return newContentObservation(observationID, "policy", "", fmt.Sprintf(
		"The requester approved this call and it has not run yet: %s with input %s. Calling %s with exactly this input runs it without asking again. Any other input is a different call, and the requester will be asked about it.",
		toolName, agentcontract.CanonicalToolInput(call.ToolInput), toolName,
	))
}
