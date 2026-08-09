package loop

import (
	"strings"
)

type progressEvent struct {
	Kind string
	Key  string
}

const maxFailureProgressSinceSuccess = 4

func progressEvents(observations []turnObservation) []progressEvent {
	events := []progressEvent{}
	seenFailures := map[string]bool{}
	failureProgressSinceSuccess := 0
	for _, observation := range observations {
		recordSuccess := func(event progressEvent) {
			events = append(events, event)
			failureProgressSinceSuccess = 0
		}
		if observation.Action == "set_quality_criteria" {
			recordSuccess(progressEvent{Kind: "quality_criteria", Key: observation.ObservationID})
		}
		if observation.Action == "continue" && !observation.Failed() && !isInspectionProgressTool(observation.Tool) {
			recordSuccess(progressEvent{Kind: "tool_success", Key: observation.ObservationID + ":" + observation.Tool})
		}
		if observation.Failed() && strings.TrimSpace(observation.AttemptFingerprint) != "" && !seenFailures[observation.AttemptFingerprint] {
			seenFailures[observation.AttemptFingerprint] = true
			if failureProgressSinceSuccess < maxFailureProgressSinceSuccess {
				events = append(events, progressEvent{Kind: "failure_fingerprint", Key: observation.AttemptFingerprint})
				failureProgressSinceSuccess++
			}
		}
		for _, attachment := range observation.Attachments {
			if strings.TrimSpace(attachment.DevicePath) != "" {
				recordSuccess(progressEvent{Kind: "attachment", Key: attachment.DevicePath})
			}
		}
		if observation.Action == "continue" && (observation.Tool == "file_write" || observation.Tool == "file_edit") && !observation.Failed() {
			recordSuccess(progressEvent{Kind: "file_rewrite", Key: observation.ToolInputKey + ":" + observation.Output.Content})
		}
	}
	return events
}

func progressEventCount(observations []turnObservation) int {
	return len(progressEvents(observations))
}

func isInspectionProgressTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "file_read", "memory_search", "site_list", "conversation_history":
		return true
	default:
		return false
	}
}
