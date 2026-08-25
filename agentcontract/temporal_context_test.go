package agentcontract

import (
	"strings"
	"testing"
	"time"
)

func TestAnUnstatedClockIsNotInvented(t *testing.T) {
	description := BuildTemporalContextDescription(time.Time{})

	if strings.Contains(description, "Now: 2") {
		t.Fatalf("the runtime was not told the date and stating one is a claim about the world it cannot make: %q", description)
	}
	if !strings.Contains(description, "never from this shell's clock") {
		t.Fatalf("the agent has to be sent to where the date actually is, and away from the one clock that is reliably wrong: %q", description)
	}
}

func TestAnEnvironmentThatStatesItsClockIsTakenAtItsWord(t *testing.T) {
	description := BuildTemporalContextDescription(time.Date(2023, 5, 18, 12, 0, 0, 0, time.UTC))

	if !strings.Contains(description, "Now: 2023-05-18") {
		t.Fatalf("a host that knows its environment's clock said so, and that is the clock: %q", description)
	}
}
