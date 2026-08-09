package loop

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func observationWithOutput(observationID string, content string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolcontract.TerminalRunToolName,
		Output:        toolcontract.ToolOutput{Content: content},
	}
}

func TestAnIdenticalOutputNamesTheObservationThatAlreadySaidIt(t *testing.T) {
	earlier := []turnObservation{
		observationWithOutput("obs-001", "Commands: login search_contacts send_text_message"),
		observationWithOutput("obs-002", "No such command 'contacts'"),
	}

	repeated := observationWithOutput("obs-003", "Commands: login search_contacts send_text_message")

	if sameAs := earlierObservationWithIdenticalOutput(earlier, repeated); sameAs != "obs-001" {
		t.Fatalf("an agent re-reading the same help needs to be told which observation already said it, got %q", sameAs)
	}
}

func TestDifferentOutputIsNotReportedAsARepeat(t *testing.T) {
	earlier := []turnObservation{observationWithOutput("obs-001", "Commands: login search_contacts")}

	fresh := observationWithOutput("obs-002", "Contact: Erika Blackburn 4226809725")

	if sameAs := earlierObservationWithIdenticalOutput(earlier, fresh); sameAs != "" {
		t.Fatalf("calling progress a repeat would stop an agent that is working, got %q", sameAs)
	}
}

func TestEmptyOutputIsNeverARepeat(t *testing.T) {
	earlier := []turnObservation{observationWithOutput("obs-001", "   ")}

	silent := observationWithOutput("obs-002", "")

	if sameAs := earlierObservationWithIdenticalOutput(earlier, silent); sameAs != "" {
		t.Fatalf("two silent commands are not the same answer twice, got %q", sameAs)
	}
}
