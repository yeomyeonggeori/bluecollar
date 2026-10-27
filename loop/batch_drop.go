package loop

import "fmt"

const (
	batchStoppedByFailure       = "an earlier call from the same response failed"
	batchStoppedByRuntimeNote   = "the runtime added a note to an earlier call from the same response"
	batchStoppedByRepeat        = "an earlier call from the same response returned exactly what an earlier call had"
	batchStoppedByLimitPressure = "the run's budget is running low"
)

type droppedCall struct {
	text  string
	count int
}

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

func dropPendingBatchedActions(state *agentTaskState, reason string, characterLimit int) {
	if len(state.PendingBatchedActions) > 0 {
		state.Observations = append(state.Observations, notRunObservation(nextObservationIDForObservations(state.Observations), state.PendingBatchedActions, reason, characterLimit))
	}
	clearPendingBatchedActions(state)
}

func notRunObservation(observationID string, dropped []turnActionDocument, reason string, characterLimit int) turnObservation {
	return newContentObservation(observationID, "not_run", "", notRunMessage(dropped, reason, characterLimit))
}

func notRunMessage(dropped []turnActionDocument, reason string, characterLimit int) string {
	message := fmt.Sprintf("Not run: %s, so the rest of that response, %d calls, was not made and changed nothing. Make again any that are still needed:", reason, len(dropped))
	calls := distinctDroppedCalls(dropped)
	for index, call := range calls {
		line := "\n- " + call.text + repetitionSuffix(call.count)
		if len(message)+len(line) > characterLimit {
			return message + fmt.Sprintf("\n- and %d more distinct calls", len(calls)-index)
		}
		message += line
	}
	return message
}

func distinctDroppedCalls(dropped []turnActionDocument) []droppedCall {
	calls := []droppedCall{}
	indexByText := map[string]int{}
	for _, action := range dropped {
		text := action.ToolName + " " + toolCallArguments(action.ToolInput)
		if index, isSeen := indexByText[text]; isSeen {
			calls[index].count++
			continue
		}
		indexByText[text] = len(calls)
		calls = append(calls, droppedCall{text: text, count: 1})
	}
	return calls
}

func repetitionSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return fmt.Sprintf(" (repeated %d times)", count)
}
