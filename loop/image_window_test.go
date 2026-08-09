package loop

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func observationCarryingAnImage(observationID string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolcontract.ImageReadToolName,
		Attachments: []toolcontract.FileAttachment{{
			Filename:      observationID + ".png",
			ContentType:   "image/png",
			ContentBase64: "aGVsbG8=",
		}},
	}
}

func TestAnImageStopsBeingResentOnceItLeavesTheWindow(t *testing.T) {
	observations := []turnObservation{observationCarryingAnImage("obs-001")}
	for range maxProgressObservations + 4 {
		observations = append(observations, turnObservation{ObservationID: "obs-filler", Action: "continue", Tool: toolcontract.TerminalRunToolName})
	}

	message := toolResultImageContextMessage(observations)

	if len(message.Parts) != 0 {
		t.Fatalf("an image the run moved on from was still being paid for on every later turn, got %d parts", len(message.Parts))
	}
}

func TestAnImageStaysWhileTheWorkIsStillAboutIt(t *testing.T) {
	observations := []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: toolcontract.TerminalRunToolName},
		observationCarryingAnImage("obs-002"),
	}

	message := toolResultImageContextMessage(observations)

	if len(message.Parts) != 1 {
		t.Fatalf("the agent asked to look at this image and has to be able to see it, got %d parts", len(message.Parts))
	}
}
