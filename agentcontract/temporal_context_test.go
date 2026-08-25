package agentcontract

import (
	"strings"
	"testing"
	"time"
)

func TestAnUnstatedClockIsNotInvented(t *testing.T) {
	description := BuildTemporalContextDescription(time.Time{})

	if description != "" {
		t.Fatalf("an unknown clock says nothing: any sentence about dates is what sends the model to the shell's wrong one, got %q", description)
	}
}

func TestAnEnvironmentThatStatesItsClockIsTakenAtItsWord(t *testing.T) {
	description := BuildTemporalContextDescription(time.Date(2023, 5, 18, 12, 0, 0, 0, time.UTC))

	if !strings.Contains(description, "Now: 2023-05-18") {
		t.Fatalf("a host that knows its environment's clock said so, and that is the clock: %q", description)
	}
}
