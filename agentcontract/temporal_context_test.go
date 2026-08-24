package agentcontract

import (
	"strings"
	"testing"
	"time"
)

func TestAnUnstatedClockSaysWhoseItIs(t *testing.T) {
	description := BuildTemporalContextDescription(time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC), time.Time{})

	if strings.Contains(description, "Current date: ") {
		t.Fatalf("unattributed, this reads as the date of the world being worked on: %q", description)
	}
	if !strings.Contains(description, "machine running you") {
		t.Fatalf("the agent has to be able to tell this clock from the one it is acting on: %q", description)
	}
}

func TestAnEnvironmentThatStatesItsClockIsTakenAtItsWord(t *testing.T) {
	description := BuildTemporalContextDescription(time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC), time.Date(2023, 5, 18, 12, 0, 0, 0, time.UTC))

	if !strings.Contains(description, "Current date: 2023-05-18") {
		t.Fatalf("a host that knows its environment's clock said so, and that is the clock: %q", description)
	}
}
