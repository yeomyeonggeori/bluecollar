package loop

import (
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func observationCarryingAnImage(observationID string) turnObservation {
	return turnObservation{
		ToolCallResult: agentcontract.ToolCallResult{ObservationID: observationID},
		Action:         "continue",
		Tool:           toolcontract.ImageReadToolName,
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
		observations = append(observations, turnObservation{ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-filler"}, Action: "continue", Tool: toolcontract.BashToolName})
	}

	message := toolResultImageContextMessage(observations)

	if len(message.Parts) != 0 {
		t.Fatalf("an image the run moved on from was still being paid for on every later turn, got %d parts", len(message.Parts))
	}
}

func TestAnImageStaysWhileTheWorkIsStillAboutIt(t *testing.T) {
	observations := []turnObservation{
		{ToolCallResult: agentcontract.ToolCallResult{ObservationID: "obs-001"}, Action: "continue", Tool: toolcontract.BashToolName},
		observationCarryingAnImage("obs-002"),
	}

	message := toolResultImageContextMessage(observations)

	if len(message.Parts) != 1 {
		t.Fatalf("the agent asked to look at this image and has to be able to see it, got %d parts", len(message.Parts))
	}
}
