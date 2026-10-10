package loop

import "github.com/yeomyeonggeori/blueprotocol/toolcontract"

const (
	batchStoppedByFailure       = "an earlier call from the same response failed"
	batchStoppedByRuntimeNote   = "the runtime added a note to an earlier call from the same response"
	batchStoppedByRepeat        = "an earlier call from the same response returned exactly what an earlier call had"
	batchStoppedByLimitPressure = "the run's budget is running low"
)

func batchStopReason(observations []turnObservation) (string, bool) {
	if len(observations) == 0 {
		return "", false
	}
	last := observations[len(observations)-1]
	switch {
	case last.Action == "policy":
		return batchStoppedByRuntimeNote, true
	case last.Failed():
		return batchStoppedByFailure, true
	case last.RepeatsObservationID != "":
		return batchStoppedByRepeat, true
	}
	return "", false
}

func dropPendingBatchedActions(state *agentTaskState, reason string) {
	for _, dropped := range state.PendingBatchedActions {
		state.Observations = append(state.Observations, notRunObservation(nextObservationIDForObservations(state.Observations), dropped, reason))
	}
	clearPendingBatchedActions(state)
}

func notRunObservation(observationID string, dropped turnActionDocument, reason string) turnObservation {
	message := "Not run: " + reason + ", so this call was not made and changed nothing. Make it again if it is still needed."
	observation := newFailureObservation(observationID, "continue", dropped.ToolName, message, toolcontract.FailurePolicyBlocked, toolcontract.FailureCodes.PolicyBlocked, "batch")
	observation.ToolInput = append([]byte{}, dropped.ToolInput...)
	return observation
}
