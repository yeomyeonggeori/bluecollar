package agentcontract

import (
	"strings"
	"testing"
)

func TestPartialDeliveryNoticeUsesTheCurrentDeliveryOutcomes(t *testing.T) {
	prompt := BuildFailureNoticePrompt(FailureReport{
		Phase: "delivery", ArtifactRequired: true,
		AttachmentFilenames: []string{"repository.png"},
		SafeFailureSummary:  "draft.png did not reach the person",
	})
	if !strings.Contains(prompt, "during this attempt") || !strings.Contains(prompt, "repository.png") || !strings.Contains(prompt, "draft.png") {
		t.Fatalf("expected both current delivery outcomes, got %s", prompt)
	}
	if strings.Contains(prompt, "earlier in this task") || strings.Contains(prompt, "further polishing was interrupted") {
		t.Fatalf("expected no earlier-version or refinement interpretation, got %s", prompt)
	}
}
