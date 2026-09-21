package taskstate

import (
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestARunningTaskRunWithNoTurnOnItIsNotActuallyRunning(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "long task")
	runningTaskRun, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if runningTaskRun.Status != agentcontract.TaskStatusRunning {
		t.Fatalf("status = %s, want running", runningTaskRun.Status)
	}

	if taskRunService.IsTaskRunActuallyRunning(runningTaskRun) {
		t.Fatal("an open attempt with no turn on it is not work in flight")
	}

	unregisterTurn := taskRunService.RegisterTaskRunCancel(runningTaskRun.TaskRunID, func() {})
	if !taskRunService.IsTaskRunActuallyRunning(runningTaskRun) {
		t.Fatal("a registered turn is work in flight")
	}

	unregisterTurn()
	if taskRunService.IsTaskRunActuallyRunning(runningTaskRun) {
		t.Fatal("a turn that returned leaves nothing working on the run")
	}
	if _, isInterrupted := taskRunService.InterruptInactiveTaskRun(runningTaskRun.TaskRunID, "runtime no longer owns this execution"); !isInterrupted {
		t.Fatal("a run nothing is working on has to be interruptible")
	}
}

func TestARunReclaimedBecauseNobodyOwnedItIsNeverResumed(t *testing.T) {
	taskRunService := NewTaskRunService(NewTaskEventService())
	taskRun := taskRunService.CreateTaskRun("person-1", "direct-1", "대체된 요청")
	if _, errorValue := taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	reclaimedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, agentcontract.TaskInterruptReasonUnownedExecution)
	if !isInterrupted {
		t.Fatal("expected a run nothing is working on to be reclaimable")
	}

	selection := taskRunService.SelectInterruptedTaskRunsForAutoResume(time.Now(), 5)

	if len(selection.SelectedTaskRuns) != 0 {
		t.Fatalf("selected task runs = %+v, want nothing re-run on a requester's behalf", selection.SelectedTaskRuns)
	}
	if reclaimedTaskRun.FailureReason != agentcontract.TaskInterruptReasonUnownedExecution {
		t.Fatalf("failure reason = %q, want the reclaim reason on the row", reclaimedTaskRun.FailureReason)
	}
	restartedTaskRun := interruptedTaskRunForTest(t, taskRunService, time.Now())
	if selection := taskRunService.SelectInterruptedTaskRunsForAutoResume(time.Now(), 5); len(selection.SelectedTaskRuns) != 1 || selection.SelectedTaskRuns[0].TaskRunID != restartedTaskRun.TaskRunID {
		t.Fatalf("selected task runs = %+v, want the restart-interrupted run still resumable", selection.SelectedTaskRuns)
	}
}

func TestTheStatusesARequesterCanStillControlHaveOneOwner(t *testing.T) {
	for _, status := range agentcontract.RequesterControllableTaskStatuses() {
		if !agentcontract.IsTaskRunRequesterControllable(status) {
			t.Fatalf("status %s is listed but not recognised", status)
		}
	}
	for _, status := range cancelTaskRunFromStates() {
		if !agentcontract.IsTaskRunRequesterControllable(status) {
			t.Fatalf("a run may be cancelled from %s, so a host must treat it as controllable", status)
		}
	}
	if len(cancelTaskRunFromStates()) != len(agentcontract.RequesterControllableTaskStatuses()) {
		t.Fatal("the cancel from-states and the controllable statuses are one set, not two that happen to match")
	}
	for _, status := range []agentcontract.TaskStatus{agentcontract.TaskStatusCompleted, agentcontract.TaskStatusFailed, agentcontract.TaskStatusCancelled, agentcontract.TaskStatusInterrupted} {
		if agentcontract.IsTaskRunRequesterControllable(status) {
			t.Fatalf("status %s is closed and takes no further requester control", status)
		}
	}
}
