package bench

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type scriptedHarness struct {
	statusByTaskID map[string]taskstate.TaskStatus
	finishMessage  string
	runError       error
	promptsSeen    []string
}

func (harness *scriptedHarness) RunTurn(_ context.Context, request agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	harness.promptsSeen = append(harness.promptsSeen, request.Prompt)
	if harness.runError != nil {
		return agentcontract.AgentTurnResult{}, harness.runError
	}
	status := harness.statusByTaskID[request.Prompt]
	if status == "" {
		status = taskstate.TaskStatusCompleted
	}
	return agentcontract.AgentTurnResult{
		TaskRun:       taskstate.TaskRun{TaskRunID: "run-" + request.Prompt, Status: status},
		FinishMessage: harness.finishMessage,
	}, nil
}

type ledgerByTaskRun map[string][]taskstate.TaskEvent

func (ledger ledgerByTaskRun) ListTaskEvent(taskRunID string) []taskstate.TaskEvent {
	return ledger[taskRunID]
}

func benchmarkTask(taskID string, prompt string, verify Verifier) Task {
	return Task{ID: taskID, Request: agentcontract.AgentTurnRequest{Prompt: prompt}, Verify: verify}
}

func TestASuiteRunMeasuresEveryTaskItDrove(t *testing.T) {
	harness := &scriptedHarness{}
	ledger := ledgerByTaskRun{"run-first": twoTurnLedger()}
	runner := NewRunner("bluecollar", harness, ledger)

	report, errorValue := runner.RunSuite(context.Background(), "smoke", []Task{
		benchmarkTask("task-a", "first", ReachedEndWithoutFailure),
		benchmarkTask("task-b", "second", ReachedEndWithoutFailure),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if report.Harness != "bluecollar" || report.Suite != "smoke" || len(report.Results) != 2 {
		t.Fatalf("expected both tasks measured under this harness, got %+v", report)
	}
	if report.Results[0].Metrics.Turns != 2 || report.Results[1].Metrics.Turns != 0 {
		t.Fatalf("expected each task measured from its own ledger, got %+v", report.Results)
	}
	if report.Results[0].Metrics.Verdict != VerdictPassed {
		t.Fatalf("expected a completed run to pass, got %q", report.Results[0].Metrics.Verdict)
	}
}

func TestATaskThatNeverFinishedFailsRatherThanScoringOnItsEfficiency(t *testing.T) {
	harness := &scriptedHarness{statusByTaskID: map[string]taskstate.TaskStatus{"first": taskstate.TaskStatusBlocked}}
	runner := NewRunner("bluecollar", harness, ledgerByTaskRun{})

	report, _ := runner.RunSuite(context.Background(), "smoke", []Task{
		benchmarkTask("task-a", "first", ReachedEndWithoutFailure),
	})

	if report.Results[0].Metrics.Verdict != VerdictFailed {
		t.Fatalf("a blocked run has not done the work, got %q", report.Results[0].Metrics.Verdict)
	}
}

func TestAHarnessThatErroredIsMeasuredAndFailedRatherThanSkipped(t *testing.T) {
	harness := &scriptedHarness{runError: errors.New("provider unavailable")}
	runner := NewRunner("bluecollar", harness, ledgerByTaskRun{})

	report, errorValue := runner.RunSuite(context.Background(), "smoke", []Task{
		benchmarkTask("task-a", "first", ReachedEndWithoutFailure),
	})

	if errorValue != nil {
		t.Fatal("one task erroring is a result, not a reason to abandon the suite")
	}
	if len(report.Results) != 1 || report.Results[0].Metrics.Verdict != VerdictFailed {
		t.Fatalf("expected the errored task recorded as failed, got %+v", report.Results)
	}
}

func TestATaskWithNoVerifierIsReportedUnverifiedRatherThanPassed(t *testing.T) {
	runner := NewRunner("bluecollar", &scriptedHarness{}, ledgerByTaskRun{})

	report, _ := runner.RunSuite(context.Background(), "smoke", []Task{
		benchmarkTask("task-a", "first", nil),
	})

	if report.Results[0].Metrics.Verdict != VerdictUnverified {
		t.Fatalf("a task nobody checked has not passed, got %q", report.Results[0].Metrics.Verdict)
	}
}

func TestAVerifierThatCannotDecideCountsAgainstTheHarness(t *testing.T) {
	runner := NewRunner("bluecollar", &scriptedHarness{}, ledgerByTaskRun{})
	failingVerifier := func(context.Context, TaskOutcome) (Verdict, error) {
		return VerdictPassed, errors.New("verifier could not reach the fixture")
	}

	report, _ := runner.RunSuite(context.Background(), "smoke", []Task{
		benchmarkTask("task-a", "first", failingVerifier),
	})

	if report.Results[0].Metrics.Verdict != VerdictFailed {
		t.Fatalf("an unprovable pass is not a pass, got %q", report.Results[0].Metrics.Verdict)
	}
}

func TestReplyContainsChecksTheAnswerRatherThanTheStatus(t *testing.T) {
	harness := &scriptedHarness{finishMessage: "deleted the 10am meeting"}
	runner := NewRunner("bluecollar", harness, ledgerByTaskRun{})

	report, _ := runner.RunSuite(context.Background(), "smoke", []Task{
		benchmarkTask("task-a", "first", ReplyContains("deleted", "10am")),
		benchmarkTask("task-b", "second", ReplyContains("scheduled")),
	})

	if report.Results[0].Metrics.Verdict != VerdictPassed || report.Results[1].Metrics.Verdict != VerdictFailed {
		t.Fatalf("expected the reply to decide each verdict, got %+v", report.Results)
	}
}

func TestARunnerWithoutALedgerRefusesRatherThanReportingEmptyMetrics(t *testing.T) {
	runner := NewRunner("bluecollar", &scriptedHarness{}, nil)

	if _, errorValue := runner.RunSuite(context.Background(), "smoke", []Task{benchmarkTask("task-a", "first", nil)}); errorValue == nil {
		t.Fatal("measuring nothing and calling it zero is worse than saying it cannot measure")
	}
}
