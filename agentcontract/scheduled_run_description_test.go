package agentcontract

import (
	"strings"
	"testing"
)

func TestAFiringIsDescribedOnceWithItsSchedule(t *testing.T) {
	description := ScheduledRunDescriptionForPrompt(ScheduledRunContext{
		ScheduleID:   "schedule-1",
		Name:         "주간 보고 알림",
		Kind:         "cron",
		Cadence:      "매일 22:08",
		OccurrenceAt: "2026-09-18T22:08:00Z",
	})

	for _, text := range []string{"Scheduled run:", "\"scheduleID\":\"schedule-1\"", "\"cadence\":\"매일 22:08\"", "\"occurrenceAt\":\"2026-09-18T22:08:00Z\""} {
		if !strings.Contains(description, text) {
			t.Fatalf("expected the firing to carry %q, got %q", text, description)
		}
	}
	if occurrences := strings.Count(description, ScheduledRunReading); occurrences != 1 {
		t.Fatalf("expected the reading exactly once, got %d in %q", occurrences, description)
	}
}

func TestNothingIsSaidAboutAFiringThatDidNotHappen(t *testing.T) {
	if description := ScheduledRunDescriptionForPrompt(ScheduledRunContext{}); description != "" {
		t.Fatalf("expected no description without a scheduled run, got %q", description)
	}
}
