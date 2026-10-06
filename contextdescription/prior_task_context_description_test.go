package contextdescription

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestPriorTaskReportsAreHypothesesAndCallsAreEvidence(t *testing.T) {
	description := PriorTaskContextDescription(agentcontract.PriorTaskContext{
		TaskRunID: "previous", Result: "Both tasks were not created.",
		RecordedAttempts: []agentcontract.PriorTaskAttempt{{ObservationID: "obs-001", Tool: "record_create"}},
	})
	for _, text := range []string{"recordedAttempts", "hypotheses", "original user messages", "current state", "obs-001"} {
		if !strings.Contains(description, text) {
			t.Fatalf("retry lost %q: %s", text, description)
		}
	}
}
