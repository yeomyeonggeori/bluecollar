package agentcontract

import "time"

type TaskStatus string

const (
	TaskStatusPlanned          TaskStatus = "planned"
	TaskStatusRunning          TaskStatus = "running"
	TaskStatusWaitingUserInput TaskStatus = "waiting_user_input"
	TaskStatusWaitingApproval  TaskStatus = "waiting_approval"
	TaskStatusBlocked          TaskStatus = "blocked"
	TaskStatusInterrupted      TaskStatus = "interrupted"
	TaskStatusCompleted        TaskStatus = "completed"
	TaskStatusFailed           TaskStatus = "failed"
	TaskStatusCancelled        TaskStatus = "cancelled"
)

const TaskInterruptReasonPlannedShutdown = "planned_shutdown"
const TaskInterruptReasonRuntimeRestart = "runtime restarted before task completed"

func TaskRunWasInterruptedByRuntimeRestart(taskRun TaskRun) bool {
	if taskRun.Status != TaskStatusInterrupted {
		return false
	}
	return taskRun.FailureReason == TaskInterruptReasonRuntimeRestart || taskRun.FailureReason == TaskInterruptReasonPlannedShutdown
}

type TaskAttemptStatus string

const (
	TaskAttemptStatusStarting    TaskAttemptStatus = "starting"
	TaskAttemptStatusRunning     TaskAttemptStatus = "running"
	TaskAttemptStatusCompleted   TaskAttemptStatus = "completed"
	TaskAttemptStatusFailed      TaskAttemptStatus = "failed"
	TaskAttemptStatusCancelled   TaskAttemptStatus = "cancelled"
	TaskAttemptStatusInterrupted TaskAttemptStatus = "interrupted"
)

type TaskRun struct {
	TaskRunID               string     `json:"taskRunID"`
	RequesterPersonID       string     `json:"requesterPersonID"`
	OriginConversationID    string     `json:"originConversationID"`
	OriginReplyTargetID     string     `json:"originReplyTargetID,omitempty"`
	OriginIsThread          bool       `json:"originIsThread,omitempty"`
	CurrentAttemptID        string     `json:"currentAttemptID,omitempty"`
	CurrentAgentProfileName string     `json:"currentAgentProfileName"`
	Status                  TaskStatus `json:"status"`
	Prompt                  string     `json:"prompt"`
	Result                  string     `json:"result"`
	FailureReason           string     `json:"failureReason"`
	CreatedAt               time.Time  `json:"createdAt"`
	UpdatedAt               time.Time  `json:"updatedAt"`
}

type TaskAttempt struct {
	TaskAttemptID string            `json:"taskAttemptID"`
	TaskRunID     string            `json:"taskRunID"`
	RunnerID      string            `json:"runnerID"`
	Status        TaskAttemptStatus `json:"status"`
	StartedAt     time.Time         `json:"startedAt"`
	FinishedAt    *time.Time        `json:"finishedAt,omitempty"`
	FailureReason string            `json:"failureReason,omitempty"`
}

type TaskEvent struct {
	TaskEventID string    `json:"taskEventID"`
	TaskRunID   string    `json:"taskRunID"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
}

type TaskRunTransition struct {
	TaskRunID               string
	FromStates              []TaskStatus
	ToState                 TaskStatus
	CurrentAgentProfileName string
	Result                  string
	FailureReason           string
	StartedAttempt          *TaskAttempt
	FinishCurrentAttempt    bool
	FinishedAttemptStatus   TaskAttemptStatus
	RunnerID                string
	Event                   *TaskEvent
	UpdatedAt               time.Time
}
