package taskstate

import (
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskScheduleKind string

const (
	TaskScheduleKindOnce     TaskScheduleKind = "once"
	TaskScheduleKindInterval TaskScheduleKind = "interval"
	TaskScheduleKindCron     TaskScheduleKind = "cron"
)

type TaskScheduleExecutionMode string

const (
	TaskScheduleExecutionModeAgent   TaskScheduleExecutionMode = "agent"
	TaskScheduleExecutionModeMessage TaskScheduleExecutionMode = "message"
)

type TaskStep struct {
	TaskStepID               string                   `json:"taskStepID"`
	TaskRunID                string                   `json:"taskRunID"`
	ParentTaskStepID         string                   `json:"parentTaskStepID"`
	AssignedAgentProfileName string                   `json:"assignedAgentProfileName"`
	Instruction              string                   `json:"instruction"`
	Status                   agentcontract.TaskStatus `json:"status"`
	Output                   string                   `json:"output"`
}

type TaskArtifact struct {
	TaskArtifactID string `json:"taskArtifactID"`
	TaskRunID      string `json:"taskRunID"`
	Name           string `json:"name"`
	Body           string `json:"body"`
}

type TaskWaitToken struct {
	WaitID         string     `json:"waitID"`
	TaskRunID      string     `json:"taskRunID"`
	PersonID       string     `json:"personID"`
	Platform       string     `json:"platform"`
	ConversationID string     `json:"conversationID"`
	ReplyTargetID  string     `json:"replyTargetID"`
	ThreadRootID   string     `json:"threadRootID"`
	DispatchID     string     `json:"dispatchID"`
	InteractionID  string     `json:"interactionID"`
	Kind           string     `json:"kind"`
	State          string     `json:"state"`
	ExpiresAt      time.Time  `json:"expiresAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
}

type TaskSession struct {
	TaskSessionID string    `json:"taskSessionID"`
	PersonID      string    `json:"personID"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type TaskSchedule struct {
	TaskScheduleID    string                    `json:"taskScheduleID"`
	CreatorPersonID   string                    `json:"creatorPersonID"`
	Name              string                    `json:"name"`
	Prompt            string                    `json:"prompt"`
	ExecutionMode     TaskScheduleExecutionMode `json:"executionMode"`
	AgentProfileName  string                    `json:"agentProfileName"`
	Platform          string                    `json:"platform"`
	ConversationID    string                    `json:"conversationID"`
	ReplyTargetID     string                    `json:"replyTargetID"`
	TimeZone          string                    `json:"timeZone"`
	Kind              TaskScheduleKind          `json:"kind"`
	RunAt             *time.Time                `json:"runAt"`
	IntervalSecond    int                       `json:"intervalSecond"`
	CronExpression    string                    `json:"cronExpression"`
	MaxRunCount       int                       `json:"maxRunCount,omitempty"`
	CompletedRunCount int                       `json:"completedRunCount"`
	ExpiresAt         *time.Time                `json:"expiresAt"`
	NextRunAt         *time.Time                `json:"nextRunAt"`
	LastRunAt         *time.Time                `json:"lastRunAt"`
	LastTaskRunID     string                    `json:"lastTaskRunID"`
	LeaseOwner        string                    `json:"leaseOwner"`
	LeasedUntil       *time.Time                `json:"leasedUntil"`
	FailureCount      int                       `json:"failureCount"`
	LastError         string                    `json:"lastError"`
	NextAttemptAt     *time.Time                `json:"nextAttemptAt"`
	CreatedAt         time.Time                 `json:"createdAt"`
	UpdatedAt         time.Time                 `json:"updatedAt"`
}
