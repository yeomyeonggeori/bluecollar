package agentcontract

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type AgentRequest struct {
	RequesterPersonID          string
	RequesterName              string
	RequesterCallingName       string
	RequesterHandle            string
	RequesterCircles           []string
	SourceReference            string
	IsApprovalContinuation     bool
	IsRuntimeRestartResume     bool
	ExistingTaskRunID          string
	OriginReplyTargetID        string
	OriginIsThread             bool
	ProfileName                string
	ConversationID             string
	ConversationType           string
	Prompt                     string
	InputParts                 []AgentPart
	ResponseLanguage           string
	VisibleContext             VisibleContext
	MemoryFacts                []MemoryFact
	ToolSet                    *toolcontract.ToolSet
	PinnedToolNames            []string
	PinnedSkillNames           []string
	WorkspaceRootPath          string
	ActivePaths                []string
	InstructionPrompt          string
	ActiveGoal                 ActiveGoal
	PriorTask                  PriorTaskContext
	ScheduledRun               ScheduledRunContext
	ActiveTask                 ActiveTaskContext
	PendingConfirmation        PendingConfirmationContext
	PendingChoice              PendingChoiceContext
	PendingInput               PendingInputContext
	TaskShape                  TaskShape
	AllowGiveUp                bool
	AllowGiveUpReason          string
	PrecomputedTurnDecision    *TurnDecision
	IsPrecomputedDecisionExact bool
	SkipSkillSelection         bool
	AmbientDuty                AmbientDutyContext
	TaskLevel                  TaskLevel
	TurnStartedAt              time.Time
	CarriedOutCalls            []CarriedOutCall
	CheckpointSender           AgentCheckpointSender
}

type ActiveTaskContext struct {
	TaskRunID string
	Prompt    string
	Status    string
	Summary   string
}

type PendingConfirmationContext struct {
	TaskRunID string
	Prompt    string
	Question  string
}

type PendingChoiceContext struct {
	TaskRunID     string
	Question      string
	SelectionMode string
	Options       []ChoiceReplyOption
}

type PendingInputContext struct {
	TaskRunID     string
	Question      string
	SelectionMode string
	Options       []ChoiceReplyOption
}

type ChoiceReplyOption struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	ShortLabel string `json:"shortLabel,omitempty"`
	Value      string `json:"value,omitempty"`
}

type ContractToolWorkingSet struct {
	RequiredNextTools     []string
	RequiredEvidenceTools []string
}

func (workingSet ContractToolWorkingSet) IsAuthoritative() bool {
	return len(workingSet.RequiredNextTools) > 0 || len(workingSet.RequiredEvidenceTools) > 0
}

type CarriedOutCall struct {
	ToolName  string                  `json:"toolName"`
	ToolInput json.RawMessage         `json:"toolInput,omitempty"`
	Result    toolcontract.ToolResult `json:"result"`
}

type AgentTurnRequest struct {
	RequesterPersonID            string
	RequesterEmail               string
	RequesterName                string
	RequesterPlatformUserID      string
	SourceReference              string
	IsApprovalContinuation       bool
	IsRuntimeRestartResume       bool
	ExistingTaskRunID            string
	OriginReplyTargetID          string
	OriginIsThread               bool
	Platform                     string
	RequesterCallingName         string
	RequesterHandle              string
	RequesterCircles             []string
	Company                      CompanyContext
	ProfileName                  string
	ConversationID               string
	ConversationType             string
	Prompt                       string
	InputParts                   []AgentPart
	ResponseLanguage             string
	VisibleContext               VisibleContext
	MemoryFacts                  []MemoryFact
	ToolSet                      *toolcontract.ToolSet
	AvailableSkills              []SkillInstruction
	PinnedToolNames              []string
	PinnedSkillNames             []string
	WorkspaceRootPath            string
	WorkspaceDefaultPath         string
	WorkspaceGuidance            []string
	AgentIdentity                AgentIdentity
	ActivePaths                  []string
	HostInstruction              string
	InstructionPrompt            string
	InstructionSources           []InstructionSource
	SkillDecisions               []SkillSelectionDecision
	SkillRetrievalMode           string
	SkillIndexStatus             string
	SkillCandidateCount          int
	SkillQueries                 []string
	ContractToolWorkingSet       ContractToolWorkingSet
	RequiredEvidenceTools        []string
	RequiredAttachmentSuffixes   []string
	OutcomeContract              OutcomeContract
	ActiveGoal                   ActiveGoal
	PriorTask                    PriorTaskContext
	ScheduledRun                 ScheduledRunContext
	ToolExposure                 ToolExposureEvent
	PrecomputedTurnDecision      *TurnDecision
	IsPrecomputedDecisionExact   bool
	SkipSkillSelection           bool
	AmbientDuty                  AmbientDutyContext
	TaskShape                    TaskShape
	TaskLevel                    TaskLevel
	TurnStartedAt                time.Time
	EffortStartedAt              time.Time
	TurnAnchorClamped            bool
	OriginalTurnStartedAt        time.Time
	CarriedOutCalls              []CarriedOutCall
	CheckpointSender             AgentCheckpointSender
	StepBudgetContext            string
	ArtifactManifest             []ArtifactManifestEntry
	RestrictActionToTerminalOnly bool
}

type AgentTurnResult struct {
	TaskRun                taskstate.TaskRun
	TurnRoute              TurnRoute
	ReactionEmojiName      string
	FinishMessage          string
	UserNotice             string
	FailureNotice          FailureNotice
	ReplySuppressed        bool
	ReplySuppressionReason string
	Attachments            []toolcontract.FileAttachment
	RecoveryActions        []toolcontract.RecoveryAction
	ToolNames              []string
}

type AgentCheckpointSender func(context.Context, AgentCheckpoint) error

type AgentCheckpoint struct {
	TaskRunID string
	Message   string
	ToolName  string
	Durable   bool
}
