package loop

import (
	"github.com/yeomyeonggeori/bluecollar/contextdescription"
	"github.com/yeomyeonggeori/bluecollar/iterationcost"
	"github.com/yeomyeonggeori/bluecollar/llmcalls"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/bluecollar/turnoptions"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var _ agentcontract.Harness = (*AgentKernel)(nil)

type (
	IterationCostObserver     = iterationcost.IterationCostObserver
	IterationCost             = iterationcost.IterationCost
	AgentIdentity             = agentcontract.AgentIdentity
	ActiveGoal                = agentcontract.ActiveGoal
	ActiveGoalStatus          = agentcontract.ActiveGoalStatus
	ActiveTaskContext         = agentcontract.ActiveTaskContext
	AgentCheckpoint           = agentcontract.AgentCheckpoint
	AgentCheckpointSender     = agentcontract.AgentCheckpointSender
	AgentFilePart             = agentcontract.AgentFilePart
	AgentImagePart            = agentcontract.AgentImagePart
	AgentPart                 = agentcontract.AgentPart
	AgentPartSource           = agentcontract.AgentPartSource
	AgentRequest              = agentcontract.AgentRequest
	AgentTurnRequest          = agentcontract.AgentTurnRequest
	CarriedOutCall            = agentcontract.CarriedOutCall
	AgentTurnResult           = agentcontract.AgentTurnResult
	ApprovalSignal            = agentcontract.ApprovalSignal
	ArtifactManifestEntry     = agentcontract.ArtifactManifestEntry
	ChoiceReplyOption         = agentcontract.ChoiceReplyOption
	ClarificationOption       = agentcontract.ClarificationOption
	CompanyContext            = agentcontract.CompanyContext
	PlanStep                  = toolcontract.PlanStep
	ToolSelector              = agentcontract.ToolSelector
	ContractToolWorkingSet    = agentcontract.ContractToolWorkingSet
	DeliverableKind           = agentcontract.DeliverableKind
	ExecutionPlan             = agentcontract.ExecutionPlan
	ExpectedResult            = agentcontract.ExpectedResult
	FailureNotice             = agentcontract.FailureNotice
	HarnessSession            = agentcontract.HarnessSession
	HeldCall                  = agentcontract.HeldCall
	InstructionBundle         = agentcontract.InstructionBundle
	InstructionSource         = agentcontract.InstructionSource
	IntakeClassification      = agentcontract.IntakeClassification
	IntakeDecision            = agentcontract.IntakeDecision
	IntakeOptions             = agentcontract.IntakeOptions
	MemoryFact                = agentcontract.MemoryFact
	OutcomeContract           = agentcontract.OutcomeContract
	OutcomeEffect             = agentcontract.OutcomeEffect
	PendingInputContext       = agentcontract.PendingInputContext
	PriorTaskContext          = agentcontract.PriorTaskContext
	PriorTaskReference        = agentcontract.PriorTaskReference
	RecoveryBudget            = turnoptions.RecoveryBudget
	ScheduledRunContext       = agentcontract.ScheduledRunContext
	SkillCandidate            = agentcontract.SkillCandidate
	SkillInstruction          = agentcontract.SkillInstruction
	SkillRetrievalResult      = agentcontract.SkillRetrievalResult
	SkillRetriever            = agentcontract.SkillRetriever
	SkillSearchQuery          = agentcontract.SkillSearchQuery
	SkillSearchQuerySet       = agentcontract.SkillSearchQuerySet
	SkillSelectionDecision    = agentcontract.SkillSelectionDecision
	TaskControlIntent         = agentcontract.TaskControlIntent
	TaskControlIntentDecision = agentcontract.TaskControlIntentDecision
	TaskLevel                 = agentcontract.TaskLevel
	TaskShape                 = agentcontract.TaskShape
	ToolExposureEvent         = agentcontract.ToolExposureEvent
	TurnDecision              = turnclassification.TurnDecision
	TurnOptions               = turnoptions.TurnOptions
	TurnRoute                 = agentcontract.TurnRoute
	VisibleContext            = agentcontract.VisibleContext
	VisibleContextMaterial    = agentcontract.VisibleContextMaterial
	VisibleContextMessage     = agentcontract.VisibleContextMessage
	droppedToolGroup          = agentcontract.DroppedToolGroup
)

const (
	ActiveGoalStatusActive           = agentcontract.ActiveGoalStatusActive
	ElapsedBudgetFromCaller          = turnoptions.ElapsedBudgetFromCaller
	ElapsedBudgetFromLevel           = turnoptions.ElapsedBudgetFromLevel
	ActiveGoalStatusBlocked          = agentcontract.ActiveGoalStatusBlocked
	ActiveGoalStatusCompleted        = agentcontract.ActiveGoalStatusCompleted
	ActiveGoalStatusWaitingApproval  = agentcontract.ActiveGoalStatusWaitingApproval
	ActiveGoalStatusWaitingUserInput = agentcontract.ActiveGoalStatusWaitingUserInput

	AgentPartTypeFile  = agentcontract.AgentPartTypeFile
	AgentPartTypeImage = agentcontract.AgentPartTypeImage
	AgentPartTypeText  = agentcontract.AgentPartTypeText

	ApprovalSignalApprove = agentcontract.ApprovalSignalApprove
	ApprovalSignalReject  = agentcontract.ApprovalSignalReject

	ArtifactRequirementNone      = agentcontract.ArtifactRequirementNone
	ArtifactRequirementPreferred = agentcontract.ArtifactRequirementPreferred
	ArtifactRequirementRequired  = agentcontract.ArtifactRequirementRequired

	DeliverableKindDocument     = agentcontract.DeliverableKindDocument
	DeliverableKindNone         = agentcontract.DeliverableKindNone
	DeliverableKindPresentation = agentcontract.DeliverableKindPresentation

	ResponseLanguageEnglish = toolcontract.ResponseLanguageEnglish
	ResponseLanguageKorean  = toolcontract.ResponseLanguageKorean

	TaskControlIntentNone    = agentcontract.TaskControlIntentNone
	TaskControlIntentStop    = agentcontract.TaskControlIntentStop
	TaskControlIntentStopAll = agentcontract.TaskControlIntentStopAll

	ExpectedResultTypeFile    = agentcontract.ExpectedResultTypeFile
	ExpectedResultTypeLink    = agentcontract.ExpectedResultTypeLink
	ExpectedResultTypeMessage = turnclassification.ExpectedResultTypeMessage

	IntakeClassificationBoundedTask       = agentcontract.IntakeClassificationBoundedTask
	IntakeClassificationNeedsConfirmation = agentcontract.IntakeClassificationNeedsConfirmation
	IntakeClassificationQuickReply        = agentcontract.IntakeClassificationQuickReply
	IntakeClassificationUnsupported       = agentcontract.IntakeClassificationUnsupported

	PriorTaskReferenceNone            = agentcontract.PriorTaskReferenceNone
	PriorTaskReferenceOutcomeRecovery = agentcontract.PriorTaskReferenceOutcomeRecovery

	TaskLevelHigh   = agentcontract.TaskLevelHigh
	TaskLevelLow    = agentcontract.TaskLevelLow
	TaskLevelMax    = agentcontract.TaskLevelMax
	TaskLevelMedium = agentcontract.TaskLevelMedium
	TaskLevelXHigh  = agentcontract.TaskLevelXHigh
	TaskLevelXLow   = agentcontract.TaskLevelXLow

	TaskShapeApprovalGatedTask = agentcontract.TaskShapeApprovalGatedTask
	TaskShapeImmediateReply    = agentcontract.TaskShapeImmediateReply
	TaskShapeMaintenanceTask   = agentcontract.TaskShapeMaintenanceTask
	TaskShapeResearchTask      = agentcontract.TaskShapeResearchTask
	TaskShapeScheduledTask     = agentcontract.TaskShapeScheduledTask

	TurnRouteAnswerMeta     = agentcontract.TurnRouteAnswerMeta
	TurnRouteAnswerQuestion = agentcontract.TurnRouteAnswerQuestion
	TurnRouteClarify        = agentcontract.TurnRouteClarify
	TurnRouteConsume        = agentcontract.TurnRouteConsume
	TurnRouteContinueTask   = agentcontract.TurnRouteContinueTask
	TurnRouteGiveUp         = agentcontract.TurnRouteGiveUp
	TurnRouteReviseTask     = agentcontract.TurnRouteReviseTask
	TurnRouteStartTask      = agentcontract.TurnRouteStartTask
)

var (
	buildFailureNoticeRepairPrompt       = agentcontract.BuildFailureNoticeRepairPrompt
	buildFailureNoticePrompt             = agentcontract.BuildFailureNoticePrompt
	failureReportAttachmentFilenames     = agentcontract.FailureReportAttachmentFilenames
	redactRawFailureNotice               = agentcontract.RedactRawFailureNotice
	LargerTaskLevel                      = turnclassification.LargerTaskLevel
	NormalizeResponseLanguage            = toolcontract.NormalizeResponseLanguage
	NormalizeTaskLevel                   = agentcontract.NormalizeTaskLevel
	OutcomeContractHasRequirements       = agentcontract.OutcomeContractHasRequirements
	VisibleSkillInstructionsForRequester = agentcontract.VisibleSkillInstructionsForRequester
	ResolveResponseLanguage              = toolcontract.ResolveResponseLanguage
	NormalizePlan                        = toolcontract.NormalizePlan
	normalizePlanSteps                   = toolcontract.NormalizePlanSteps

	ObservationIDFromContext     = toolcontract.ObservationIDFromContext
	ResponseLanguageFromContext  = toolcontract.ResponseLanguageFromContext
	TaskRunIDFromContext         = toolcontract.TaskRunIDFromContext
	UserFacingMessageFromContext = toolcontract.UserFacingMessageFromContext
	WithObservationID            = toolcontract.WithObservationID
	WithResponseLanguage         = toolcontract.WithResponseLanguage
	WithTaskRunID                = toolcontract.WithTaskRunID
	WithUserFacingMessage        = toolcontract.WithUserFacingMessage

	appendUniqueStrings         = toolcontract.AppendUniqueStrings
	taskLevelRank               = turnclassification.TaskLevelRank
	normalizeExpectedResults    = turnclassification.NormalizeExpectedResults
	normalizePriorTaskReference = turnclassification.NormalizePriorTaskReference
)

var buildVisibleContextDescription = agentcontract.BuildVisibleContextDescription

const (
	MemoryScopeWorkspace = agentcontract.MemoryScopeWorkspace
)

var buildMemoryContext = agentcontract.BuildMemoryContext

var (
	buildTemporalContextDescription = agentcontract.BuildTemporalContextDescription
	companyLocation                 = agentcontract.CompanyLocation
)

var (
	responseLanguageInstruction = agentcontract.ResponseLanguageInstruction
	redactUnsafeText            = RedactUnsafeText
)

type (
	FailureReport                 = agentcontract.FailureReport
	FailureNoticeGenerator        = agentcontract.FailureNoticeGenerator
	FailureNoticeGenerationStatus = agentcontract.FailureNoticeGenerationStatus
	IntakeReport                  = agentcontract.IntakeReport
)

var (
	diagnosticEventID              = agentcontract.DiagnosticEventID
	failureNoticeMessageIsSendable = agentcontract.FailureNoticeMessageIsSendable
	buildRawErrorFailureNotice     = agentcontract.BuildRawErrorFailureNotice
)

var (
	elapsedLimitRawErrorSummary            = ElapsedLimitRawErrorSummary
	textExceedsCharacterBudget             = agentcontract.TextExceedsCharacterBudget
	finishMessageMaximumCharacters         = agentcontract.FinishMessageMaximumCharacters
	buildFinishMessageCompressionPrompt    = BuildFinishMessageCompressionPrompt
	generateRecoveryChatText               = agentcontract.GenerateRecoveryChatText
	recoveryContextError                   = agentcontract.RecoveryContextError
	buildElapsedLimitRawErrorFailureNotice = BuildElapsedLimitRawErrorFailureNotice
	buildFailureNotice                     = agentcontract.BuildFailureNotice
)

var (
	activeGoalDescriptionForPrompt = contextdescription.ActiveGoalDescriptionForPrompt
)

var scheduledRunDescriptionForPrompt = agentcontract.ScheduledRunDescriptionForPrompt

var (
	normalizeIntakeOptions          = turnclassification.NormalizeIntakeOptions
	normalizeRequestedOutputFormats = turnclassification.NormalizeRequestedOutputFormats
	registeredToolNamesOnly         = turnclassification.RegisteredToolNamesOnly
)

type (
	TaskLevelProfile = iterationcost.TaskLevelProfile
)

var (
	TaskLevelProfileForLevel       = iterationcost.TaskLevelProfileForLevel
	NewIterationCostObserver       = iterationcost.NewIterationCostObserver
	DurationForIterationCount      = iterationcost.DurationForIterationCount
	nextTaskLevel                  = iterationcost.NextTaskLevel
	taskLevelRequiresPlan          = iterationcost.TaskLevelRequiresPlan
	taskLevelWantsSingleFinalReply = iterationcost.TaskLevelWantsSingleFinalReply
)

type (
	llmCallRecord    = agentcontract.LLMCallRecord
	llmCallObserver  = agentcontract.LLMCallObserver
	intakeCallLedger = llmcalls.IntakeCallLedger
)

var observeLanguageModel = agentcontract.ObserveLanguageModel

const agentActionSchemaName = llmcalls.AgentActionSchemaName

const turnRouterSchemaName = llmcalls.TurnRouterSchemaName
