package bench

import (
	"context"
	"errors"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type Task struct {
	ID      string
	Request agentcontract.AgentTurnRequest
	Verify  Verifier
}

type Verifier func(context.Context, TaskOutcome) (Verdict, error)

type TaskOutcome struct {
	TaskID     string
	TurnResult agentcontract.AgentTurnResult
	TaskEvents []taskstate.TaskEvent
	RunError   error
}

type TaskEventReader interface {
	ListTaskEvent(taskRunID string) []taskstate.TaskEvent
}

type Runner struct {
	harness         agentcontract.Harness
	taskEventReader TaskEventReader
	harnessName     string
}

func NewRunner(harnessName string, harness agentcontract.Harness, taskEventReader TaskEventReader) *Runner {
	return &Runner{harnessName: strings.TrimSpace(harnessName), harness: harness, taskEventReader: taskEventReader}
}

func (runner *Runner) RunSuite(ctx context.Context, suite string, tasks []Task) (SuiteReport, error) {
	if runner.harness == nil || runner.taskEventReader == nil {
		return SuiteReport{}, errors.New("a benchmark run needs a harness to drive and a ledger to read back")
	}
	report := SuiteReport{Suite: suite, Harness: runner.harnessName}
	for _, task := range tasks {
		report.Results = append(report.Results, runner.runTask(ctx, suite, task))
	}
	return report, nil
}

func (runner *Runner) runTask(ctx context.Context, suite string, task Task) TaskResult {
	turnResult, runError := runner.harness.RunTurn(ctx, task.Request)
	taskRunID := turnResult.TaskRun.TaskRunID
	taskEvents := runner.taskEventReader.ListTaskEvent(taskRunID)
	metrics := MeasureTaskRun(taskRunID, taskEvents)
	metrics.Verdict = verdictFor(ctx, task, TaskOutcome{
		TaskID:     task.ID,
		TurnResult: turnResult,
		TaskEvents: taskEvents,
		RunError:   runError,
	})
	return TaskResult{Suite: suite, TaskID: task.ID, Metrics: metrics}
}

func verdictFor(ctx context.Context, task Task, outcome TaskOutcome) Verdict {
	if task.Verify == nil {
		return VerdictUnverified
	}
	verdict, errorValue := task.Verify(ctx, outcome)
	if errorValue != nil {
		return VerdictFailed
	}
	return verdict
}

func ReachedEndWithoutFailure(_ context.Context, outcome TaskOutcome) (Verdict, error) {
	if outcome.RunError != nil {
		return VerdictFailed, nil
	}
	if outcome.TurnResult.TaskRun.Status != taskstate.TaskStatusCompleted {
		return VerdictFailed, nil
	}
	return VerdictPassed, nil
}

func ReplyContains(expectedFragments ...string) Verifier {
	return func(_ context.Context, outcome TaskOutcome) (Verdict, error) {
		reply := outcome.TurnResult.FinishMessage
		for _, expectedFragment := range expectedFragments {
			if !strings.Contains(reply, expectedFragment) {
				return VerdictFailed, nil
			}
		}
		return VerdictPassed, nil
	}
}
