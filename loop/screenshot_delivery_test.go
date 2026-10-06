package loop

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func screenshotDeliveryObservations() []turnObservation {
	capture := newContentObservation("obs-001", "continue", "browser_screenshot", "image captured")
	capture.Attachments = []toolcontract.FileAttachment{{DevicePath: "capture.png", Filename: "capture.png", ContentType: "image/png"}}
	delivery := newContentObservation("obs-002", "continue", toolcontract.FileDeliverToolName, "file staged")
	delivery.Attachments = []toolcontract.FileAttachment{{DevicePath: "/workspace/repository.png", Filename: "repository.png", ContentType: "image/png"}}
	return []turnObservation{capture, delivery, delivery}
}

func TestScreenshotEvidenceCarriesOnlyTheExplicitReplyFile(t *testing.T) {
	observations := screenshotDeliveryObservations()
	for _, references := range [][]completionEvidenceReference{
		nil,
		{{ObservationID: "obs-001"}, {ObservationID: "obs-002"}},
	} {
		finish := finishClaimingSatisfied("Repository screenshot attached.")
		finish.CompletionEvidence = references
		result := validateCompletionFacts(AgentTurnRequest{}, observations, finish)
		if !result.IsSatisfied || len(result.Attachments) != 1 || result.Attachments[0].Filename != "repository.png" {
			t.Fatalf("expected only the staged file, got %+v", result)
		}
	}
}

func TestScreenshotEvidenceIsNotAnUnsentFileAfterResume(t *testing.T) {
	observations := screenshotDeliveryObservations()
	attachments := attachmentsFromObservations(observations)
	if len(attachments) != 1 || attachments[0].Filename != "repository.png" {
		t.Fatalf("expected resumed state to carry only the staged file, got %+v", attachments)
	}
	remaining := attachmentsNotYetDelivered(attachments, []string{"/workspace/repository.png"})
	if len(remaining) != 0 {
		t.Fatalf("expected no files left after delivery, got %+v", remaining)
	}
}

func TestScreenshotCaptureStaysAvailableAsModelEvidence(t *testing.T) {
	capture := screenshotDeliveryObservations()[0]
	if len(progressAttachments(capture)) != 1 {
		t.Fatal("expected the capture to remain available to the model")
	}
	if len(deliveredAttachments([]turnObservation{capture})) != 0 {
		t.Fatal("expected a capture alone to stage no reply files")
	}
}
