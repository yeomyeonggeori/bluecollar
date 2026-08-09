package bench

import (
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func taskEventAt(second int, name string, body string) taskstate.TaskEvent {
	return taskstate.TaskEvent{
		Name:      name,
		Body:      body,
		CreatedAt: time.Date(2026, 8, 7, 0, 0, second, 0, time.UTC),
	}
}

func twoTurnLedger() []taskstate.TaskEvent {
	return []taskstate.TaskEvent{
		taskEventAt(0, "task.created", "send the summary"),
		taskEventAt(1, "task.running", "assistant"),
		taskEventAt(2, "llm.call", `{"schemaName":"bluecollar_agent_turn_action","model":"openai/gpt-5.6-luna","latencyMs":5000,"promptTokens":16000,"completionTokens":120,"cachedPromptTokens":15000,"totalTokens":16120,"costUSD":0.002}`),
		taskEventAt(3, "agent.action", `{"action":"continue","toolName":"message_send"}`),
		taskEventAt(4, "tool.message_send.requested", `{"observationID":"obs-001"}`),
		taskEventAt(5, "tool.message_send.result", `{"observationID":"obs-001","output":{"content":"sent"}}`),
		taskEventAt(6, "llm.call", `{"schemaName":"bluecollar_agent_turn_action","model":"openai/gpt-5.6-luna","latencyMs":4000,"promptTokens":18000,"completionTokens":80,"totalTokens":18080,"costUSD":0.003}`),
		taskEventAt(7, "agent.action", `{"action":"finish"}`),
		taskEventAt(10, "task.completed", "sent the summary"),
	}
}

func TestARunIsMeasuredFromTheLedgerItAlreadyWrote(t *testing.T) {
	metrics := MeasureTaskRun("task-1", twoTurnLedger())

	if metrics.Turns != 2 || metrics.ToolCalls != 1 || metrics.LanguageModelCalls != 2 {
		t.Fatalf("expected two turns, one tool call and two model calls, got %+v", metrics)
	}
	if metrics.PromptTokens != 34000 || metrics.CompletionTokens != 200 || metrics.CachedPromptTokens != 15000 {
		t.Fatalf("expected token totals summed across calls, got %+v", metrics)
	}
	if metrics.CostUSD != 0.005 || metrics.ModelLatencyMS != 9000 {
		t.Fatalf("expected cost and latency summed across calls, got cost=%v latency=%v", metrics.CostUSD, metrics.ModelLatencyMS)
	}
	if metrics.TerminalStatus != string(taskstate.TaskStatusCompleted) || !metrics.ReachedEnd {
		t.Fatalf("expected the run to be recorded as finished, got %+v", metrics)
	}
	if metrics.WallClockMS != 10000 {
		t.Fatalf("expected wall clock across the whole ledger, got %d", metrics.WallClockMS)
	}
}

func TestPromptTokensPerTurnIsWhatSeparatesTwoHarnessesOnOneModel(t *testing.T) {
	metrics := MeasureTaskRun("task-1", twoTurnLedger())

	if metrics.PromptTokensPerTurn != 17000 {
		t.Fatalf("expected 34000 prompt tokens over two turns, got %v", metrics.PromptTokensPerTurn)
	}
}

func TestPromptBytesPerTurnSurvivesAServerThatMisreportsItsUsage(t *testing.T) {
	metrics := MeasureTaskRun("task-1", []taskstate.TaskEvent{
		taskEventAt(0, "task.created", "do it"),
		taskEventAt(1, "llm.call", `{"promptBytes":40000,"promptTokens":3}`),
		taskEventAt(2, "agent.action", `{"action":"continue"}`),
		taskEventAt(3, "llm.call", `{"promptBytes":60000,"promptTokens":3}`),
		taskEventAt(4, "agent.action", `{"action":"finish"}`),
	})

	if metrics.PromptBytesPerTurn != 50000 {
		t.Fatalf("a token count only the server can give is not a measurement, expected 50000 bytes per turn, got %v", metrics.PromptBytesPerTurn)
	}
}

func TestATurnlessRunReportsNoPerTurnCostRatherThanDividingByZero(t *testing.T) {
	metrics := MeasureTaskRun("task-1", []taskstate.TaskEvent{
		taskEventAt(0, "task.created", "do nothing"),
		taskEventAt(1, "task.failed", "launch failed"),
	})

	if metrics.PromptTokensPerTurn != 0 || metrics.Turns != 0 {
		t.Fatalf("expected no per-turn figure without a turn, got %+v", metrics)
	}
	if metrics.TerminalStatus != string(taskstate.TaskStatusFailed) {
		t.Fatalf("expected the failure to be the terminal status, got %q", metrics.TerminalStatus)
	}
}

func TestACallHeldForApprovalIsNotCountedAsAFailedToolCall(t *testing.T) {
	metrics := MeasureTaskRun("task-1", []taskstate.TaskEvent{
		taskEventAt(0, "task.created", "send it"),
		taskEventAt(1, "agent.action", `{"action":"continue","toolName":"message_send"}`),
		taskEventAt(2, "tool.message_send.requested", `{"observationID":"obs-001"}`),
		taskEventAt(3, "tool.message_send.result", `{"observationID":"obs-001","failure":{"code":"interaction_required","requiresApproval":true}}`),
		taskEventAt(4, "approval.pending_call", `{"toolName":"message_send"}`),
	})

	if metrics.FailedToolCalls != 0 {
		t.Fatalf("a call waiting for the requester is not a call that failed, got %+v", metrics)
	}
	if metrics.ApprovalHolds != 1 {
		t.Fatalf("expected the hold to be counted on its own, got %+v", metrics)
	}
}

func TestARealFailedToolCallIsCounted(t *testing.T) {
	metrics := MeasureTaskRun("task-1", []taskstate.TaskEvent{
		taskEventAt(0, "task.created", "read it"),
		taskEventAt(1, "tool.file_read.requested", `{"observationID":"obs-001"}`),
		taskEventAt(2, "tool.file_read.result", `{"observationID":"obs-001","failure":{"code":"not_found"}}`),
	})

	if metrics.FailedToolCalls != 1 {
		t.Fatalf("expected the failed read to be counted, got %+v", metrics)
	}
}

func TestAMeasuredRunCarriesNoVerdictUntilABenchmarkGivesOne(t *testing.T) {
	metrics := MeasureTaskRun("task-1", twoTurnLedger())

	if metrics.Verdict != VerdictUnverified {
		t.Fatalf("the ledger knows what ran, not whether it was right, got %q", metrics.Verdict)
	}
	if metrics.WithVerdict(VerdictPassed).Verdict != VerdictPassed {
		t.Fatal("expected a benchmark to be able to record its own verdict")
	}
}

func TestAnUnfinishedRunIsNotReportedAsHavingReachedTheEnd(t *testing.T) {
	metrics := MeasureTaskRun("task-1", []taskstate.TaskEvent{
		taskEventAt(0, "task.created", "send it"),
		taskEventAt(1, "agent.action", `{"action":"continue"}`),
		taskEventAt(2, "task.paused", "may I?"),
	})

	if metrics.ReachedEnd || metrics.TerminalStatus != "" {
		t.Fatalf("a paused run has not finished, got %+v", metrics)
	}
}
