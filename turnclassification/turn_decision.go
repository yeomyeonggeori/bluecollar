package turnclassification

import (
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

type TurnDecision struct {
	Route                   agentcontract.TurnRoute             `json:"route"`
	Classification          agentcontract.IntakeClassification  `json:"classification"`
	TaskShape               agentcontract.TaskShape             `json:"taskShape"`
	TaskLevel               agentcontract.TaskLevel             `json:"level"`
	RequestedOutputFormats  []string                            `json:"requestedOutputFormats"`
	DeliverableKind         agentcontract.DeliverableKind       `json:"deliverableKind,omitempty"`
	ExpectedResults         []agentcontract.ExpectedResult      `json:"expectedResults,omitempty"`
	ResponseLanguage        string                              `json:"responseLanguage"`
	Reason                  string                              `json:"reason"`
	UserFacingReply         string                              `json:"userFacingReply"`
	IsExternalSendRequested bool                                `json:"isExternalSendRequested"`
	InitialToolNames        []string                            `json:"initialToolNames,omitempty"`
	PriorTaskReference      agentcontract.PriorTaskReference    `json:"priorTaskReference,omitempty"`
	Approval                *agentcontract.ApprovalSignal       `json:"approval,omitempty"`
	Choices                 []string                            `json:"choices,omitempty"`
	ClarificationQuestion   string                              `json:"clarificationQuestion,omitempty"`
	ClarificationOptions    []agentcontract.ClarificationOption `json:"clarificationOptions,omitempty"`
	HasIndependentWork      bool                                `json:"hasIndependentWork"`
	ExpectedToolCount       agentcontract.ExpectedToolCount     `json:"expectedToolCount,omitempty"`
	RoutingFallbackReason   string                              `json:"routingFallbackReason,omitempty"`
}

func (turnDecision TurnDecision) WithTurnWords(turnWords TurnWords) TurnDecision {
	turnDecision.Reason = turnWords.Reason
	turnDecision.UserFacingReply = turnWords.UserFacingReply
	turnDecision.ClarificationQuestion = turnWords.ClarificationQuestion
	turnDecision.ClarificationOptions = turnWords.ClarificationOptions
	turnDecision.ExpectedResults = turnWords.ExpectedResults
	return turnDecision
}

func (turnDecision TurnDecision) IntakeDecision() agentcontract.IntakeDecision {
	return agentcontract.IntakeDecision{
		Classification:          turnDecision.Classification,
		TaskShape:               turnDecision.TaskShape,
		TaskLevel:               agentcontract.NormalizeTaskLevel(string(turnDecision.TaskLevel)),
		RequestedOutputFormats:  append([]string{}, turnDecision.RequestedOutputFormats...),
		DeliverableKind:         turnDecision.DeliverableKind,
		ExpectedResults:         NormalizeExpectedResults(turnDecision.ExpectedResults),
		ResponseLanguage:        turnDecision.ResponseLanguage,
		Reason:                  turnDecision.Reason,
		UserFacingReply:         turnDecision.UserFacingReply,
		IsExternalSendRequested: turnDecision.IsExternalSendRequested,
		HasIndependentWork:      turnDecision.HasIndependentWork,
		ExpectedToolCount:       turnDecision.ExpectedToolCount,
		InitialToolNames:        append([]string{}, turnDecision.InitialToolNames...),
		PriorTaskReference:      NormalizePriorTaskReference(turnDecision.PriorTaskReference),
		ClarificationQuestion:   turnDecision.ClarificationQuestion,
		ClarificationOptions:    append([]agentcontract.ClarificationOption{}, turnDecision.ClarificationOptions...),
	}
}

func (turnDecision TurnDecision) WithRestoredIntakeState(intakeDecision agentcontract.IntakeDecision) TurnDecision {
	if agentcontract.NormalizeTaskLevel(string(intakeDecision.TaskLevel)) == "" {
		return turnDecision
	}
	turnDecision.Classification = intakeDecision.Classification
	turnDecision.TaskShape = intakeDecision.TaskShape
	turnDecision.TaskLevel = intakeDecision.TaskLevel
	turnDecision.RequestedOutputFormats = append([]string{}, intakeDecision.RequestedOutputFormats...)
	turnDecision.ExpectedResults = NormalizeExpectedResults(intakeDecision.ExpectedResults)
	turnDecision.IsExternalSendRequested = intakeDecision.IsExternalSendRequested
	turnDecision.HasIndependentWork = intakeDecision.HasIndependentWork
	turnDecision.ExpectedToolCount = intakeDecision.ExpectedToolCount
	turnDecision.InitialToolNames = append([]string{}, intakeDecision.InitialToolNames...)
	return turnDecision
}

type ClarificationDisposition string

const ClarificationDispositionAsk ClarificationDisposition = "ask"

const ClarificationDispositionStartWork ClarificationDisposition = "start_work"

type TurnWords struct {
	Reason                   string                              `json:"reason"`
	UserFacingReply          string                              `json:"userFacingReply"`
	ClarificationDisposition ClarificationDisposition            `json:"clarificationDisposition,omitempty"`
	ClarificationQuestion    string                              `json:"clarificationQuestion"`
	ClarificationOptions     []agentcontract.ClarificationOption `json:"clarificationOptions"`
	ExpectedResults          []agentcontract.ExpectedResult      `json:"expectedResults"`
}

func NormalizePriorTaskReference(reference agentcontract.PriorTaskReference) agentcontract.PriorTaskReference {
	if agentcontract.IsPriorTaskReferenceName(string(reference)) {
		return reference
	}
	return agentcontract.PriorTaskReferenceNone
}
