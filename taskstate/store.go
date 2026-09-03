package taskstate

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

// The harness records what a task did; the host decides where that record lives.

type TaskRunStore interface {
	AdvanceTaskRun(taskRunID string, currentAgentProfileName string) (agentcontract.TaskRun, error)
	AppendTaskEvent(taskRunID string, name string, body string)
	CancelActiveTaskRuns(request TaskRunCancelRequest) []agentcontract.TaskRun
	CancelTaskRunWithReason(taskRunID string, requesterPersonID string, reason string) (agentcontract.TaskRun, error)
	CompleteTaskRun(taskRunID string, result string) (agentcontract.TaskRun, error)
	CreateTaskRunWithOrigin(requesterPersonID string, origin TaskRunOrigin, prompt string) agentcontract.TaskRun
	CreateTaskRunWithOriginAndError(requesterPersonID string, origin TaskRunOrigin, prompt string) (agentcontract.TaskRun, error)
	FailTaskRun(taskRunID string, reason string) (agentcontract.TaskRun, error)
	FindTaskRun(taskRunID string) (agentcontract.TaskRun, bool)
	InterruptInactiveTaskRun(taskRunID string, reason string) (agentcontract.TaskRun, bool)
	IsTaskRunActuallyRunning(taskRun agentcontract.TaskRun) bool
	ListTaskEvent(taskRunID string) []agentcontract.TaskEvent
	ListTaskRun() []agentcontract.TaskRun
	ListTaskRunByPersonID(personID string) []agentcontract.TaskRun
	PauseTaskRun(taskRunID string, status agentcontract.TaskStatus, reason string) (agentcontract.TaskRun, error)
	RecordTaskRunResult(taskRunID string, result string) (agentcontract.TaskRun, error)
	RegisterTaskRunCancel(taskRunID string, cancelFunction context.CancelFunc) func()
	RegisterTaskRunObserver(taskRunID string, observer func(RawTurnEvent)) func()
	RegisterTaskRunTool(taskRunID string, observationID string, toolName string) func()
	ResumeTaskRun(taskRunID string) (agentcontract.TaskRun, error)
}

type TaskStepStore interface {
	AddTaskStep(taskStep TaskStep)
}

type TaskArtifactStore interface {
	AddTaskArtifactBody(taskRunID string, name string, body string) TaskArtifact
	ListTaskArtifact(taskRunID string) []TaskArtifact
}
