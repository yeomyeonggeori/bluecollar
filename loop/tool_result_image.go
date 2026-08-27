package loop

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

// An image the agent read is carried to the model as bytes, and those bytes are
// deliberately not written to the task ledger — the picture is already there
// once, in the tool result that produced it. So a turn that comes back from an
// approval pause or a restart holds the attachment without its content, and the
// model would be told about a picture it cannot see. The file the tool read is
// still where it was: this reads it again.
type ToolResultImageSource interface {
	LoadImageContentBase64(ctx context.Context, taskRunID string, devicePath string) (string, error)
}

type imageRehydrationOutcome struct {
	DevicePath string
	Failure    string
}

func rehydrateImageAttachments(
	ctx context.Context,
	taskRunID string,
	observations []turnObservation,
	source ToolResultImageSource,
) []imageRehydrationOutcome {
	if source == nil {
		return nil
	}
	outcomes := []imageRehydrationOutcome{}
	for _, observation := range observationsShowingTheirImages(observations) {
		for index, attachment := range observation.Attachments {
			if !imageAttachmentNeedsItsContent(attachment) {
				continue
			}
			content, errorValue := source.LoadImageContentBase64(ctx, taskRunID, strings.TrimSpace(attachment.DevicePath))
			if errorValue != nil {
				outcomes = append(outcomes, imageRehydrationOutcome{DevicePath: attachment.DevicePath, Failure: errorValue.Error()})
				continue
			}
			if strings.TrimSpace(content) == "" {
				outcomes = append(outcomes, imageRehydrationOutcome{DevicePath: attachment.DevicePath, Failure: "the file held no content"})
				continue
			}
			observation.Attachments[index].ContentBase64 = content
			outcomes = append(outcomes, imageRehydrationOutcome{DevicePath: attachment.DevicePath})
		}
	}
	return outcomes
}

func (agentTurnRunner *AgentTurnRunner) bringBackImagesTheTurnAlreadyRead(
	ctx context.Context,
	taskRunID string,
	observations []turnObservation,
) {
	outcomes := rehydrateImageAttachments(ctx, taskRunID, observations, agentTurnRunner.toolResultImageSource)
	if len(outcomes) == 0 {
		return
	}
	broughtBack := []string{}
	lost := []map[string]string{}
	for _, outcome := range outcomes {
		if outcome.Failure == "" {
			broughtBack = append(broughtBack, outcome.DevicePath)
			continue
		}
		lost = append(lost, map[string]string{"devicePath": outcome.DevicePath, "reason": outcome.Failure})
	}
	agentTurnRunner.appendEvent(taskRunID, "agent.tool_result_images_restored", marshalEventBody(map[string]any{
		"broughtBack": broughtBack,
		"lost":        lost,
	}))
}

func imageAttachmentNeedsItsContent(attachment toolcontract.FileAttachment) bool {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attachment.ContentType)), "image/") {
		return false
	}
	return strings.TrimSpace(attachment.ContentBase64) == "" && strings.TrimSpace(attachment.DevicePath) != ""
}
