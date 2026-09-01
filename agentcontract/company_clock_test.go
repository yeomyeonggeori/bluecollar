package agentcontract

import (
	"strings"
	"testing"
	"time"
)

func TestTheCompanyNamesTheZoneTheAgentReadsTheClockIn(t *testing.T) {
	noon := time.Date(2026, 5, 12, 8, 32, 27, 0, time.UTC)

	inSeoul := BuildTemporalContextDescription(noon, "Asia/Seoul")
	inLosAngeles := BuildTemporalContextDescription(noon, "America/Los_Angeles")

	if !strings.Contains(inSeoul, "Now: 2026-05-12 (Tue) 17:32 +09:00 Asia/Seoul") {
		t.Fatalf("expected the hour where the company is, got %s", inSeoul)
	}
	if !strings.Contains(inLosAngeles, "Now: 2026-05-12 (Tue) 01:32 -07:00 America/Los_Angeles") {
		t.Fatalf("a company west of UTC reads its own clock, got %s", inLosAngeles)
	}
}

func TestACompanyThatNamesNoZoneLeavesTheMachineItRunsOn(t *testing.T) {
	if CompanyLocation("") != time.Local {
		t.Fatal("a company that has not said where it is leaves the machine's own zone")
	}
	if CompanyLocation("Not/AZone") != time.Local {
		t.Fatal("a zone nothing can load leaves the machine's own zone")
	}
}

func TestAMessageIsStampedWhereTheCompanyIs(t *testing.T) {
	sentAt := time.Date(2026, 7, 10, 5, 3, 0, 0, time.UTC)

	if stamp := FormatContextTimestamp(sentAt, "Asia/Seoul"); stamp != "2026-07-10 14:03" {
		t.Fatalf("expected the company's wall clock, got %q", stamp)
	}
	if stamp := FormatContextTimestamp(sentAt, "America/Los_Angeles"); stamp != "2026-07-09 22:03" {
		t.Fatalf("expected the company's wall clock, got %q", stamp)
	}
}
