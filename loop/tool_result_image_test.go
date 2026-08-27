package loop

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type imageSourceHolding struct {
	contentByDevicePath map[string]string
	asked               []string
}

func (source *imageSourceHolding) LoadImageContentBase64(_ context.Context, _ string, devicePath string) (string, error) {
	source.asked = append(source.asked, devicePath)
	content, isHeld := source.contentByDevicePath[devicePath]
	if !isHeld {
		return "", errors.New("no such file")
	}
	return content, nil
}

func observationWhoseImageLostItsContent() turnObservation {
	return turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          toolcontract.ImageReadToolName,
		Attachments: []toolcontract.FileAttachment{{
			DevicePath:  "/workspace/private/people/somebody/inbox/buzz/dm/image",
			Filename:    "image",
			ContentType: "image/png",
		}},
	}
}

// A turn that comes back from an approval pause or a restart holds the
// attachment without its bytes, because the bytes are deliberately not written
// to the ledger. The file is still where the tool read it.
func TestAnImageComesBackFromTheFileItWasReadFrom(t *testing.T) {
	observations := []turnObservation{observationWhoseImageLostItsContent()}
	source := &imageSourceHolding{contentByDevicePath: map[string]string{
		"/workspace/private/people/somebody/inbox/buzz/dm/image": "aGVsbG8=",
	}}

	outcomes := rehydrateImageAttachments(context.Background(), "task-1", observations, source)

	if observations[0].Attachments[0].ContentBase64 != "aGVsbG8=" {
		t.Fatal("expected the image to come back")
	}
	if len(outcomes) != 1 || outcomes[0].Failure != "" {
		t.Fatalf("expected one quiet success, got %+v", outcomes)
	}
	if len(toolResultImageContextMessage(observations).Parts) != 1 {
		t.Fatal("expected the restored image to reach the prompt")
	}
}

func TestAnImageAlreadyInHandIsNotReadAgain(t *testing.T) {
	observations := []turnObservation{observationWhoseImageLostItsContent()}
	observations[0].Attachments[0].ContentBase64 = "aGVsbG8="
	source := &imageSourceHolding{contentByDevicePath: map[string]string{}}

	rehydrateImageAttachments(context.Background(), "task-1", observations, source)

	if len(source.asked) != 0 {
		t.Fatalf("expected no file to be read, asked for %v", source.asked)
	}
}

// Losing the file is worth saying out loud; the turn goes on without the image
// rather than stopping, and the outcome names what was lost.
func TestAnImageThatCannotBeReadIsNamed(t *testing.T) {
	observations := []turnObservation{observationWhoseImageLostItsContent()}

	outcomes := rehydrateImageAttachments(context.Background(), "task-1", observations, &imageSourceHolding{contentByDevicePath: map[string]string{}})

	if len(outcomes) != 1 || outcomes[0].Failure == "" {
		t.Fatalf("expected the loss to be named, got %+v", outcomes)
	}
	if outcomes[0].DevicePath != "/workspace/private/people/somebody/inbox/buzz/dm/image" {
		t.Fatalf("expected the file to be named, got %q", outcomes[0].DevicePath)
	}
}

func TestWithoutASourceNothingIsRead(t *testing.T) {
	observations := []turnObservation{observationWhoseImageLostItsContent()}

	if len(rehydrateImageAttachments(context.Background(), "task-1", observations, nil)) != 0 {
		t.Fatal("expected no outcome without a source")
	}
}

// This is the round trip the earlier test never made: the observation goes
// through the ledger and comes back, and the image has to survive it.
func TestAnImageSurvivesTheLedgerRoundTripOnceItIsBroughtBack(t *testing.T) {
	original := observationWhoseImageLostItsContent()
	original.Attachments[0].ContentBase64 = "aGVsbG8="

	restored, errorValue := decodeTurnObservation([]byte(marshalEventBody(original)))
	if errorValue != nil {
		t.Fatalf("expected the observation to come back from the ledger: %v", errorValue)
	}
	if restored.Attachments[0].ContentBase64 != "" {
		t.Fatal("expected the ledger to hold no second copy of the image")
	}

	observations := []turnObservation{restored}
	rehydrateImageAttachments(context.Background(), "task-1", observations, &imageSourceHolding{contentByDevicePath: map[string]string{
		"/workspace/private/people/somebody/inbox/buzz/dm/image": "aGVsbG8=",
	}})

	if len(toolResultImageContextMessage(observations).Parts) != 1 {
		t.Fatal("expected the image to reach the prompt after the round trip")
	}
}
