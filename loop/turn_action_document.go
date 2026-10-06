package loop

import "encoding/json"

type turnActionDocument struct {
	Action                string                        `json:"action"`
	Message               string                        `json:"message"`
	Attachments           []replyAttachment             `json:"attachments,omitempty"`
	ExpectsAnswer         bool                          `json:"expectsAnswer,omitempty"`
	Choices               []string                      `json:"choices,omitempty"`
	Final                 bool                          `json:"final,omitempty"`
	AssistantText         string                        `json:"assistantText,omitempty"`
	ModelReasoning        string                        `json:"modelReasoning,omitempty"`
	ModelReasoningField   string                        `json:"modelReasoningField,omitempty"`
	ToolName              string                        `json:"toolName"`
	ToolInput             json.RawMessage               `json:"toolInput"`
	Reason                string                        `json:"reason"`
	FailureResolution     string                        `json:"failureResolution"`
	GoalStatus            string                        `json:"goalStatus"`
	GoalSatisfied         *bool                         `json:"goalSatisfied"`
	HasRemainingWork      bool                          `json:"hasRemainingWork"`
	CompletionEvidenceIDs []string                      `json:"completionEvidenceIDs"`
	CompletionEvidence    []completionEvidenceReference `json:"-"`
	Instruction           string                        `json:"instruction,omitempty"`
	ExpectedResult        string                        `json:"expectedResult,omitempty"`
	QualityCriteria       []string                      `json:"qualityCriteria"`
	QualityReview         []qualityReviewItem           `json:"qualityReview"`
	UsedFailureFacts      failureReportFacts            `json:"usedFailureFacts"`
	ExecutionStateUpdate  ExecutionState                `json:"executionStateUpdate"`
	BatchedActions        []turnActionDocument          `json:"batchedActions,omitempty"`
}

type replyAttachment struct {
	Path     string `json:"path"`
	Filename string `json:"filename,omitempty"`
}

func takeBatchedAction(state *agentTaskState) (turnActionDocument, bool) {
	if len(state.PendingBatchedActions) == 0 {
		clearPendingBatchedToolExposure(state)
		return turnActionDocument{}, false
	}
	nextAction := state.PendingBatchedActions[0]
	state.PendingBatchedActions = state.PendingBatchedActions[1:]
	if len(state.PendingBatchedActions) == 0 {
		clearPendingBatchedToolExposure(state)
	}
	return nextAction, true
}

func rememberBatchedActions(state *agentTaskState, actionDocument turnActionDocument, exposedToolNames []string, exposure ToolExposureEvent) {
	if lastObservationFailed(state.Observations) {
		clearPendingBatchedActions(state)
		return
	}
	state.PendingBatchedActions = append(state.PendingBatchedActions, actionDocument.BatchedActions...)
	if len(actionDocument.BatchedActions) > 0 {
		state.PendingBatchedToolNames = append([]string{}, exposedToolNames...)
		state.PendingBatchedToolExposure = exposure
	}
}

func clearPendingBatchedToolExposure(state *agentTaskState) {
	state.PendingBatchedToolNames = nil
	state.PendingBatchedToolExposure = ToolExposureEvent{}
}

func clearPendingBatchedActions(state *agentTaskState) {
	state.PendingBatchedActions = nil
	clearPendingBatchedToolExposure(state)
}
