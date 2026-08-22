package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func callObservation(observationID string, toolName string, toolInputKey string) turnObservation {
	return turnObservation{
		ObservationID: observationID,
		Action:        "continue",
		Tool:          toolName,
		ToolInputKey:  toolInputKey,
	}
}

func TestBookkeepingBetweenTwoIdenticalCallsDoesNotLaunderTheLoop(t *testing.T) {
	searchKey := "terminal_run\x00{\"command\":\"grep -r needle .\"}"
	observations := []turnObservation{
		callObservation("obs-1", toolcontract.TerminalRunToolName, searchKey),
		callObservation("obs-2", toolcontract.PlanUpdateToolName, "plan_update\x00{\"steps\":[]}"),
		callObservation("obs-3", toolcontract.TerminalRunToolName, searchKey),
	}

	if count := consecutiveIdenticalToolCallCount(observations, searchKey); count != 2 {
		t.Fatalf("updating a plan between two identical searches is still two identical searches, got %d", count)
	}
}

func TestADifferentCallBreaksTheChain(t *testing.T) {
	searchKey := "terminal_run\x00{\"command\":\"grep -r needle .\"}"
	observations := []turnObservation{
		callObservation("obs-1", toolcontract.TerminalRunToolName, searchKey),
		callObservation("obs-2", toolcontract.TerminalRunToolName, "terminal_run\x00{\"command\":\"ls\"}"),
		callObservation("obs-3", toolcontract.TerminalRunToolName, searchKey),
	}

	if count := consecutiveIdenticalToolCallCount(observations, searchKey); count != 1 {
		t.Fatalf("an agent that tried something else in between is working, not looping, got %d", count)
	}
}

func TestACallThatKeptFailingStillCounts(t *testing.T) {
	deniedKey := "file_write\x00{\"path\":\"/etc/passwd\"}"
	denied := callObservation("obs-1", toolcontract.FileWriteToolName, deniedKey)
	denied.Failure = &toolcontract.ToolFailure{Kind: toolcontract.FailurePolicyBlocked, Code: "policy_blocked"}
	observations := []turnObservation{denied, denied, denied}

	if count := consecutiveIdenticalToolCallCount(observations, deniedKey); count != 3 {
		t.Fatalf("hammering a call that keeps being refused is exactly the loop worth breaking, got %d", count)
	}
}

func TestTheThirdIdenticalCallEarnsANudge(t *testing.T) {
	searchKey := "terminal_run\x00{\"command\":\"grep -r needle .\"}"
	call := callObservation("obs-3", toolcontract.TerminalRunToolName, searchKey)
	observations := []turnObservation{
		callObservation("obs-1", toolcontract.TerminalRunToolName, searchKey),
		callObservation("obs-2", toolcontract.TerminalRunToolName, searchKey),
		call,
	}

	reminder, count, hasReminder := toolRepeatReminderObservation(observations, call)

	if !hasReminder || count != 3 {
		t.Fatalf("three identical calls in a row is the point of saying something, got count %d", count)
	}
	if strings.TrimSpace(reminder.ContentText()) == "" {
		t.Fatal("a reminder with no words tells the model nothing")
	}
	if reminder.Failed() {
		t.Fatal("the advice is not a failure: the call itself was allowed to run and its own result stands")
	}
}

func TestTheSecondIdenticalCallIsLeftAlone(t *testing.T) {
	searchKey := "terminal_run\x00{\"command\":\"grep -r needle .\"}"
	call := callObservation("obs-2", toolcontract.TerminalRunToolName, searchKey)
	observations := []turnObservation{callObservation("obs-1", toolcontract.TerminalRunToolName, searchKey), call}

	if _, _, hasReminder := toolRepeatReminderObservation(observations, call); hasReminder {
		t.Fatal("checking something twice is ordinary work, not a loop")
	}
}

func TestALongInputIsQuotedShortButComparedWhole(t *testing.T) {
	longInput := "file_write\x00{\"text\":\"" + strings.Repeat("x", 4000) + "\"}"
	call := callObservation("obs-5", toolcontract.FileWriteToolName, longInput)
	observations := []turnObservation{call, call, call, call, call}

	reminder, count, hasReminder := toolRepeatReminderObservation(observations, call)

	if !hasReminder || count != 5 {
		t.Fatalf("a long payload must not hide a loop from the chain, got count %d", count)
	}
	if len(reminder.ContentText()) > 2000 {
		t.Fatalf("quoting the whole repeated payload back would cost more context than the loop it is warning about, got %d characters", len(reminder.ContentText()))
	}
	if !strings.Contains(reminder.ContentText(), "more characters") {
		t.Fatalf("an agent reading a shortened input needs to know it was shortened, got %q", reminder.ContentText())
	}
}

func toolSetDeclaringPlanUpdateWithoutSideEffect(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{
		{Name: toolcontract.PlanUpdateToolName, SideEffectClass: toolcontract.ToolSideEffectNone, Visibility: toolcontract.ToolVisibilityModel},
		{Name: toolcontract.TerminalRunToolName, SideEffectClass: toolcontract.ToolSideEffectStateChange, Visibility: toolcontract.ToolVisibilityModel},
	})
}

func TestAPlanResubmittedAcrossRealWorkStillChangedNothing(t *testing.T) {
	toolSet := toolSetDeclaringPlanUpdateWithoutSideEffect(t)
	observations := []turnObservation{
		callObservation("obs-1", toolcontract.PlanUpdateToolName, "plan_update\x00{\"steps\":[]}"),
		callObservation("obs-2", toolcontract.TerminalRunToolName, "terminal_run\x00{\"command\":\"ls\"}"),
	}
	repeated := callObservation("obs-3", toolcontract.PlanUpdateToolName, "plan_update\x00{\"steps\":[]}")
	repeated.RepeatsObservationID = "obs-1"

	reminder, hasReminder := unchangedResultReminderObservation(toolSet, observations, repeated)
	if !hasReminder {
		t.Fatal("doing work between two identical plans does not make the second plan a change, and nothing else in the loop tells the agent")
	}
	if !strings.Contains(reminder.Output.Content, "obs-1") {
		t.Fatalf("the reminder has to name the earlier result it matched, got %q", reminder.Output.Content)
	}
}

func TestARepeatedResultFromAToolThatDoesSomethingIsNotANoOp(t *testing.T) {
	toolSet := toolSetDeclaringPlanUpdateWithoutSideEffect(t)
	repeated := callObservation("obs-3", toolcontract.TerminalRunToolName, "terminal_run\x00{\"command\":\"ls\"}")
	repeated.RepeatsObservationID = "obs-1"

	if _, hasReminder := unchangedResultReminderObservation(toolSet, nil, repeated); hasReminder {
		t.Fatal("a command that prints the same thing twice may have changed the machine both times")
	}
}
