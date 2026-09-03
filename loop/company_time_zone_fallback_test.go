package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func companyTimeZoneFallbackEvents(t *testing.T, timeZone string) []agentcontract.TaskEvent {
	t.Helper()
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.appendCompanyTimeZoneFallbackEvent("task-run-company-zone", CompanyContext{TimeZone: timeZone})
	fallbackEvents := []agentcontract.TaskEvent{}
	for _, taskEvent := range taskRunService.ListTaskEvent("task-run-company-zone") {
		if taskEvent.Name == agentcontract.TaskEventAgentCompanyTimeZoneFallback {
			fallbackEvents = append(fallbackEvents, taskEvent)
		}
	}
	return fallbackEvents
}

func TestACompanyThatNamesNoZoneLeavesTheEventThatSaysSo(t *testing.T) {
	fallbackEvents := companyTimeZoneFallbackEvents(t, "")
	if len(fallbackEvents) != 1 {
		t.Fatalf("expected one company time zone fallback event, got %d", len(fallbackEvents))
	}
	if !strings.Contains(fallbackEvents[0].Body, agentcontract.CompanyZoneFallbackUnset) {
		t.Fatalf("expected the fallback reason in the ledger, got %s", fallbackEvents[0].Body)
	}
}

func TestAZoneNothingCanLoadLeavesTheEventThatSaysSo(t *testing.T) {
	fallbackEvents := companyTimeZoneFallbackEvents(t, "Not/AZone")
	if len(fallbackEvents) != 1 {
		t.Fatalf("expected one company time zone fallback event, got %d", len(fallbackEvents))
	}
	if !strings.Contains(fallbackEvents[0].Body, agentcontract.CompanyZoneFallbackUnloadable) {
		t.Fatalf("expected the fallback reason in the ledger, got %s", fallbackEvents[0].Body)
	}
}

func TestACompanyThatNamesItsZoneLeavesNoDiagnostic(t *testing.T) {
	if fallbackEvents := companyTimeZoneFallbackEvents(t, "Asia/Seoul"); len(fallbackEvents) != 0 {
		t.Fatalf("a company that named its zone guessed nothing, got %d events", len(fallbackEvents))
	}
}
