package loop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestTheReportTheUserSeesSaysWhatHappened(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-006",
		Action:        "continue",
		Tool:          "bash",
		Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureUnknown, Code: toolcontract.FailureCodes.OperationFailed.String(),
			Stage: "bash", UserSafeSummary: "the command exited 1",
		},
	}
	observation.Output.Content = "Your Venmo balance does not have $91.00 to make this transaction"

	line := latestSafeFailureSummary([]turnObservation{observation}, "something went wrong")

	if !strings.Contains(line, "does not have $91.00") {
		t.Fatalf("an exit status is not something a user can act on: %q", line)
	}
}

func TestAReportWhoseSummaryIsAlreadyTheReasonSaysItOnce(t *testing.T) {
	observation := turnObservation{
		ObservationID: "obs-007",
		Action:        "continue",
		Tool:          "task_add",
		Failure: &toolcontract.ToolFailure{
			Kind: toolcontract.FailureInvalidInput, Code: toolcontract.FailureCodes.InvalidInput.String(),
			Stage: "task_add", UserSafeSummary: "a title is required",
		},
	}
	observation.Output.Content = "a title is required"

	if line := latestSafeFailureSummary([]turnObservation{observation}, "fallback"); line != "a title is required" {
		t.Fatalf("expected the reason once, got %q", line)
	}
}

func TestALongRunsReportKeepsItsLatestWorkWhenItMustDropSome(t *testing.T) {
	observations := []turnObservation{newContentObservation("obs-001", "continue", "plan", "build pending")}
	for index := 2; index <= 200; index++ {
		observations = append(observations, newContentObservation(fmt.Sprintf("obs-%03d", index), "continue", "bash", fmt.Sprintf("checked page %d. ", index)+strings.Repeat("all clear. ", 200)))
	}
	observations = append(observations, newContentObservation("obs-201", "continue", "bash", "build/deck.pptx written, 10 slides"))

	summary := buildLimitObservationSummary(observations)

	if !strings.Contains(summary, "build/deck.pptx written") {
		t.Fatalf("expected the latest work in the report, got %q", summary[len(summary)-300:])
	}
	if strings.Contains(summary, "build pending") {
		t.Fatal("expected the earliest observation to be the one left out")
	}
	if !strings.HasPrefix(summary, "…") {
		t.Fatalf("expected the report to open by saying earlier observations are not shown, got %q", summary[:120])
	}
}
