package taskstate

import (
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
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
