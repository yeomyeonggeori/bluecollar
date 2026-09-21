package taskstate

import (
	"testing"

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
