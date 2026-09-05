package agentcontract

import (
	"strings"
	"testing"
)

func TestPriorTaskReportsAreHypothesesAndCallsAreEvidence(t *testing.T) {
	description := PriorTaskContextDescription(PriorTaskContext{
		TaskRunID: "previous", Result: "Both tasks were not created.",
		RecordedAttempts: []PriorTaskAttempt{{ObservationID: "obs-001", Tool: "record_create"}},
	})
	for _, text := range []string{"recordedAttempts", "hypotheses", "original user messages", "current state", "obs-001"} {
		if !strings.Contains(description, text) {
			t.Fatalf("retry lost %q: %s", text, description)
		}
	}
}
