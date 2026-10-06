package loop

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type AgentKernel struct {
	iterationCostObserver   *IterationCostObserver
	taskRunService          taskstate.TaskRunStore
	taskStepService         taskstate.TaskStepStore
	taskArtifactService     taskstate.TaskArtifactStore
	languageModel           model.LanguageModelProvider
	maxTaskLanguageModel    model.LanguageModelProvider
	xHighTaskLanguageModel  model.LanguageModelProvider
	highTaskLanguageModel   model.LanguageModelProvider
	mediumTaskLanguageModel model.LanguageModelProvider
	lowTaskLanguageModel    model.LanguageModelProvider
	xLowTaskLanguageModel   model.LanguageModelProvider
	intakeLanguageModel     model.LanguageModelProvider
	turnOptions             TurnOptions
	intakeOptions           IntakeOptions
	instructionPrompt       string
	instructionSources      []InstructionSource
	instructionLoader       func() InstructionBundle
	skillRetriever          SkillRetriever
	toolSelector            ToolSelector
	companyProvider         func() CompanyContext
	toolResultSpillStore    ToolResultSpillStore
	toolResultImageSource   ToolResultImageSource
	decisionModel           model.DecisionModel
}

func NewAgentKernel(taskRunService taskstate.TaskRunStore, taskStepService taskstate.TaskStepStore) *AgentKernel {
	return &AgentKernel{
		iterationCostObserver: NewIterationCostObserver(),
		taskRunService:        taskRunService,
		taskStepService:       taskStepService,
		taskArtifactService:   taskstate.NewTaskArtifactService(),
	}
}

func (agentKernel *AgentKernel) UseLanguageModelProvider(languageModel model.LanguageModelProvider) {
	agentKernel.languageModel = languageModel
}

func (agentKernel *AgentKernel) UseTaskTierLanguageModels(taskTierLanguageModels agentcontract.TaskTierLanguageModels) {
	agentKernel.maxTaskLanguageModel = taskTierLanguageModels.Max
	agentKernel.xHighTaskLanguageModel = taskTierLanguageModels.XHigh
	agentKernel.highTaskLanguageModel = taskTierLanguageModels.High
	agentKernel.mediumTaskLanguageModel = taskTierLanguageModels.Medium
	agentKernel.lowTaskLanguageModel = taskTierLanguageModels.Low
	agentKernel.xLowTaskLanguageModel = taskTierLanguageModels.XLow
}

func (agentKernel *AgentKernel) UseTaskArtifactService(taskArtifactService taskstate.TaskArtifactStore) {
	if taskArtifactService != nil {
		agentKernel.taskArtifactService = taskArtifactService
	}
}

func (agentKernel *AgentKernel) UseToolResultSpillStore(toolResultSpillStore ToolResultSpillStore) {
	agentKernel.toolResultSpillStore = toolResultSpillStore
}

func (agentKernel *AgentKernel) UseToolResultImageSource(toolResultImageSource ToolResultImageSource) {
	agentKernel.toolResultImageSource = toolResultImageSource
}

func (agentKernel *AgentKernel) UseDecisionModel(decisionModel model.DecisionModel) {
	agentKernel.decisionModel = decisionModel
}

func (agentKernel *AgentKernel) UseTurnOptions(turnOptions TurnOptions) {
	agentKernel.turnOptions = normalizeTurnOptions(turnOptions)
}

func (agentKernel *AgentKernel) UseIntakeLanguageModelProvider(languageModel model.LanguageModelProvider) {
	agentKernel.intakeLanguageModel = languageModel
}

func (agentKernel *AgentKernel) UseIntakeOptions(intakeOptions IntakeOptions) {
	agentKernel.intakeOptions = normalizeIntakeOptions(intakeOptions)
}

func (agentKernel *AgentKernel) UseInstructionPrompt(instructionPrompt string) {
	agentKernel.instructionPrompt = strings.TrimSpace(instructionPrompt)
}

func (agentKernel *AgentKernel) UseInstructionBundle(instructionBundle InstructionBundle) {
	agentKernel.instructionPrompt = strings.TrimSpace(instructionBundle.Prompt)
	agentKernel.instructionSources = append([]InstructionSource{}, instructionBundle.Sources...)
}

func (agentKernel *AgentKernel) UseInstructionBundleLoader(instructionLoader func() InstructionBundle) {
	agentKernel.instructionLoader = instructionLoader
	if instructionLoader != nil {
		agentKernel.UseInstructionBundle(instructionLoader())
	}
}

func (agentKernel *AgentKernel) UseToolSelector(toolSelector ToolSelector) {
	agentKernel.toolSelector = toolSelector
}

func (agentKernel *AgentKernel) UseSkillRetriever(skillRetriever SkillRetriever) {
	agentKernel.skillRetriever = skillRetriever
}

func (agentKernel *AgentKernel) UseCompanyProvider(companyProvider func() CompanyContext) {
	agentKernel.companyProvider = companyProvider
}

func (agentKernel *AgentKernel) companyContext() CompanyContext {
	if agentKernel.companyProvider == nil {
		return CompanyContext{}
	}
	return agentKernel.companyProvider()
}

func (agentKernel *AgentKernel) RefreshSkillIndex(ctx context.Context, instructionBundle InstructionBundle) {
	if agentKernel.skillRetriever == nil {
		return
	}
	agentKernel.skillRetriever.Refresh(ctx, instructionBundle.Skills)
}

func (agentKernel *AgentKernel) RunTurn(responseContext context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	return agentKernel.RunPlannedTurn(responseContext, request, turnclassification.Routing{})
}

func (agentKernel *AgentKernel) RunPlannedTurn(responseContext context.Context, request AgentTurnRequest, routing turnclassification.Routing) (AgentTurnResult, error) {
	return agentKernel.RunAgentRequest(responseContext, routing, AgentRequest{
		RequesterPersonID:          request.RequesterPersonID,
		RequesterName:              request.RequesterName,
		AgentIdentity:              request.AgentIdentity,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.RequesterCircles...),
		SourceReference:            request.SourceReference,
		IsRuntimeRestartResume:     request.IsRuntimeRestartResume,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		IsTaskRunOpenedForThisTurn: request.IsTaskRunOpenedForThisTurn,
		OriginReplyTargetID:        request.OriginReplyTargetID,
		OriginIsThread:             request.OriginIsThread,
		ProfileName:                request.ProfileName,
		ConversationID:             request.ConversationID,
		Prompt:                     request.Prompt,
		InputParts:                 append([]AgentPart{}, request.InputParts...),
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		MemoryFacts:                request.MemoryFacts,
		ToolSet:                    request.ToolSet,
		PinnedToolNames:            append([]string{}, request.PinnedToolNames...),
		LikelyToolNames:            append([]string{}, request.LikelyToolNames...),
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          request.WorkspaceRootPath,
		ActivePaths:                request.ActivePaths,
		ActiveGoal:                 request.ActiveGoal,
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		SkipSkillSelection:         request.SkipSkillSelection,
		TaskLevel:                  request.TaskLevel,
		TurnStartedAt:              request.TurnStartedAt,
		ExecutionStartedAt:         request.ExecutionStartedAt,
		EnvironmentNow:             request.EnvironmentNow,
		CarriedOutCalls:            request.CarriedOutCalls,
		CheckpointSender:           request.CheckpointSender,
		TaskRunChosen:              request.TaskRunChosen,
	})
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, routing turnclassification.Routing, request AgentRequest) (AgentTurnResult, error) {
	requestReceivedAt := time.Now()
	routerCallLedger := &intakeCallLedger{}
	request.ActiveGoal = normalizePersistedActiveGoal(request.ActiveGoal)
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = requestReceivedAt.Add(-2 * time.Second)
	}
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage, request.VisibleContext.ResponseLanguage)
	if strings.TrimSpace(request.ActiveGoal.RestoreError) != "" {
		return agentKernel.CompleteLaunchFailure(responseContext, launchFailureRequest(request), "restore_state", "active_goal", errors.New(request.ActiveGoal.RestoreError)), nil
	}
	baseInstructionBundle := agentKernel.currentInstructionBundle()
	instructionBundle := baseInstructionBundle
	turnToolSet := request.ToolSet
	intakeRequest := request
	intakeRequest.ToolSet = turnToolSet
	turnDecision, errorValue := routedTurnDecision(routing)
	if errorValue != nil {
		result := agentKernel.completeTurnRouterFailure(responseContext, intakeRequest, errorValue, routerCallLedger.Records)
		return result, nil
	}
	intakeDecision := turnDecision.IntakeDecision()
	intakeDecision = promoteArtifactTaskLevelForRequest(routing, intakeRequest, intakeDecision)
	turnOptions := agentKernel.turnOptionsForIntakeDecision(responseContext, intakeDecision)
	taskBudget := newTurnBudgetContext(responseContext, executionBudgetStartedAt(request), request.IsRuntimeRestartResume, requestReceivedAt, turnOptions)
	defer taskBudget.cancel()
	taskContext := taskBudget.workContext
	request.TurnStartedAt = taskBudget.turnStartedAt
	if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, routing, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.Records); didExpire {
		return result, nil
	}
	request.ResponseLanguage = ResolveResponseLanguage(intakeDecision.ResponseLanguage, request.ResponseLanguage)
	manualPinnedToolNames := append([]string{}, request.PinnedToolNames...)
	request = restorePersistedToolSelection(request)
	persistedPinnedToolNames := append([]string{}, request.PinnedToolNames...)
	intakeRequest = request
	if turnDecision.Route == TurnRouteStartTask {
		if strings.TrimSpace(request.ExistingTaskRunID) == strings.TrimSpace(request.ActiveGoal.TaskRunID) {
			request.ExistingTaskRunID = ""
			request.IsRuntimeRestartResume = false
			intakeRequest.ExistingTaskRunID = ""
			intakeRequest.IsRuntimeRestartResume = false
		}
		request.ActiveGoal = ActiveGoal{}
		intakeRequest.ActiveGoal = ActiveGoal{}
		request, intakeDecision = applyPriorTaskOutcomeRecovery(request, intakeDecision)
		intakeDecision.InitialToolNames = registeredToolNamesOnly(turnToolSet, intakeDecision.InitialToolNames)
		intakeRequest.ActiveGoal = request.ActiveGoal
	}
	lifecycleMode := taskLifecycleModeForRequest(turnDecision, request)
	if lifecycleMode == taskLifecycleSemanticRevision {
		request.ActiveGoal = ActiveGoal{}
		intakeRequest.ActiveGoal = ActiveGoal{}
		if !request.IsTaskRunOpenedForThisTurn {
			request.ExistingTaskRunID = ""
			request.IsRuntimeRestartResume = false
			intakeRequest.ExistingTaskRunID = ""
			intakeRequest.IsRuntimeRestartResume = false
		}
	}
	startsNewSemanticRun := lifecycleMode == taskLifecycleFresh || lifecycleMode == taskLifecycleSemanticRevision
	request.LikelyToolNames = appendUniqueStrings(nil, intakeDecision.InitialToolNames...)
	request.PinnedToolNames = appendUniqueStrings(append([]string{}, request.PinnedToolNames...), intakeDecision.InitialToolNames...)
	intakeRequest.PinnedToolNames = request.PinnedToolNames
	intakeRequest.LikelyToolNames = request.LikelyToolNames
	if turnDecision.Route == TurnRouteConsume {
		result, errorValue := agentKernel.completeConsumedRequest(intakeRequest, turnDecision, routerCallLedger.Records)
		return result, errorValue
	}
	if !request.SkipSkillSelection {
		instructionBundle, intakeDecision = agentKernel.selectInstructionBundleForResolvedRequest(taskContext, baseInstructionBundle, request, intakeDecision)
	}
	if instructionBundle.ContractSkillArbitrationFailed {
		agentKernel.taskRunService.AppendTaskEvent(request.ExistingTaskRunID, agentcontract.TaskEventAgentContractArbitrationDegraded, marshalEventBody(map[string]string{
			"reason": "contract skill arbitration failed; continuing with score-selected skills",
		}))
	}
	if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, routing, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.Records); didExpire {
		return result, nil
	}
	request.PinnedToolNames = pinnedToolNamesForResolvedRequest(
		manualPinnedToolNames,
		persistedPinnedToolNames,
		intakeDecision.InitialToolNames,
		instructionBundle.RequiredEvidenceTools,
		startsNewSemanticRun,
	)
	intakeRequest.PinnedToolNames = request.PinnedToolNames
	request.PinnedSkillNames = appendUniqueStrings(request.PinnedSkillNames, selectedSkillNameList(instructionBundle.SkillDecisions)...)
	intakeRequest.PinnedSkillNames = request.PinnedSkillNames
	if intakeDecision.Classification == IntakeClassificationNeedsConfirmation {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(taskContext, routing, intakeRequest, intakeDecision, agentcontract.TaskStatusWaitingUserInput, routerCallLedger.Records)
		result.TurnRoute = turnDecision.Route
		return result, errorValue
	}
	if intakeDecision.Classification == IntakeClassificationUnsupported {
		result, errorValue := agentKernel.completeIntakeOnlyRequest(taskContext, routing, intakeRequest, intakeDecision, agentcontract.TaskStatusBlocked, routerCallLedger.Records)
		result.TurnRoute = turnDecision.Route
		return result, errorValue
	}

	requiredNextToolNames := requiredNextToolNamesForResolvedRequest(request.ActiveGoal, instructionBundle.RequiredNextTools)
	request.ActiveGoal.RequiredNextTools = requiredNextToolNames
	intakeRequest.ActiveGoal = request.ActiveGoal
	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)
	evidenceHints := selectedEvidenceHintTools(instructionBundle)
	confirmationEvidenceHints := confirmationEvidenceHintsForRequest(request, intakeDecision, evidenceHints)
	confirmationPlan, errorValue := agentKernel.planConfirmationGate(taskContext, request, intakeDecision, confirmationEvidenceHints)
	if errorValue != nil {
		if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, routing, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.Records); didExpire {
			return result, nil
		}
		return AgentTurnResult{}, errorValue
	}
	if confirmationPlan.DegradedError != nil {
		agentKernel.taskRunService.AppendTaskEvent(strings.TrimSpace(request.ExistingTaskRunID), agentcontract.TaskEventAgentConfirmationPlanDegraded, marshalEventBody(map[string]string{
			"reason": "execution plan generation failed twice; continuing with the runtime tool approval gate: " + confirmationPlan.DegradedError.Error(),
		}))
	}
	if confirmationPlan.Decision.RequiresClarification {
		confirmationResult, pauseError := agentKernel.pauseForClarification(taskContext, request, intakeDecision, confirmationPlan, OutcomeContract{}, confirmationEvidenceHints, selectedSkillNameList(instructionBundle.SkillDecisions))
		if pauseError != nil && taskBudget.didWorkExpire() {
			intakeRequest.ExistingTaskRunID = confirmationResult.TaskRun.TaskRunID
			confirmationResult = agentKernel.completeIntakeElapsed(taskBudget, routing, intakeRequest, intakeDecision, routerCallLedger.Records)
			pauseError = nil
		}
		confirmationResult.TurnRoute = turnDecision.Route
		return confirmationResult, pauseError
	}
	executionPlan := confirmationPlan.ExecutionPlan
	hasExecutionPlan := confirmationPlan.HasExecutionPlan
	outcomeContract := outcomeContractForRequest(request, intakeDecision, instructionBundle, executionPlan, hasExecutionPlan, requiredAttachmentSuffixes)
	outcomeContract = dischargeResolvedInputContract(request, turnDecision, outcomeContract)
	outcomeContract = contractReducedToCallableTools(request.ToolSet, outcomeContract)
	if result, didExpire := agentKernel.completeIntakeIfElapsed(taskBudget, routing, intakeRequest, intakeDecision, turnDecision.Route, routerCallLedger.Records); didExpire {
		return result, nil
	}
	requiredNextToolNames = requiredNextToolNamesForResolvedRequest(request.ActiveGoal, instructionBundle.RequiredNextTools)
	request.ActiveGoal.RequiredNextTools = requiredNextToolNames
	requiredEvidenceTools := outcomeContract.RequiredEvidenceTools
	requiredAttachmentSuffixes = outcomeContract.RequiredAttachmentSuffixes
	request.PinnedToolNames = pinnedToolNamesForResolvedRequest(
		manualPinnedToolNames,
		persistedPinnedToolNames,
		intakeDecision.InitialToolNames,
		requiredEvidenceTools,
		startsNewSemanticRun,
	)
	intakeRequest.PinnedToolNames = request.PinnedToolNames

	contractToolWorkingSet := ContractToolWorkingSet{
		RequiredNextTools:     requiredNextToolNames,
		RequiredEvidenceTools: append([]string{}, instructionBundle.RequiredEvidenceTools...),
	}
	turnRequest := AgentTurnRequest{
		RequesterPersonID:          request.RequesterPersonID,
		Company:                    agentKernel.companyContext(),
		RequesterName:              request.RequesterName,
		AgentIdentity:              request.AgentIdentity,
		RequesterCallingName:       request.RequesterCallingName,
		RequesterHandle:            request.RequesterHandle,
		RequesterCircles:           append([]string{}, request.RequesterCircles...),
		SourceReference:            request.SourceReference,
		IsRuntimeRestartResume:     request.IsRuntimeRestartResume,
		ExistingTaskRunID:          request.ExistingTaskRunID,
		OriginReplyTargetID:        request.OriginReplyTargetID,
		OriginIsThread:             request.OriginIsThread,
		ProfileName:                normalizedAgentProfileName(request.ProfileName),
		ConversationID:             request.ConversationID,
		Prompt:                     request.Prompt,
		InputParts:                 append([]AgentPart{}, request.InputParts...),
		ResponseLanguage:           request.ResponseLanguage,
		VisibleContext:             request.VisibleContext,
		MemoryFacts:                request.MemoryFacts,
		ToolSet:                    turnToolSet,
		AvailableSkills:            append([]SkillInstruction{}, instructionBundle.Skills...),
		PinnedToolNames:            append([]string{}, request.PinnedToolNames...),
		LikelyToolNames:            append([]string{}, request.LikelyToolNames...),
		PinnedSkillNames:           append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:          request.WorkspaceRootPath,
		InstructionPrompt:          instructionBundle.Prompt,
		InstructionSources:         append([]InstructionSource{}, instructionBundle.Sources...),
		SkillDecisions:             append([]SkillSelectionDecision{}, instructionBundle.SkillDecisions...),
		SkillRetrievalMode:         instructionBundle.RetrievalMode,
		SkillIndexStatus:           instructionBundle.IndexStatus,
		SkillCandidateCount:        instructionBundle.CandidateCount,
		SkillQueries:               append([]string{}, instructionBundle.SkillQueries...),
		ContractToolWorkingSet:     contractToolWorkingSet,
		RequiredEvidenceTools:      requiredEvidenceTools,
		RequiredAttachmentSuffixes: requiredAttachmentSuffixes,
		OutcomeContract:            outcomeContract,
		ActiveGoal:                 activeGoalForTurn(request, outcomeContract, executionPlan, hasExecutionPlan),
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		TaskShape:                  intakeDecision.TaskShape,
		TaskLevel:                  intakeDecision.TaskLevel,
		TurnStartedAt:              request.TurnStartedAt,
		EnvironmentNow:             request.EnvironmentNow,
		EffortStartedAt:            request.TurnStartedAt,
		TurnAnchorClamped:          taskBudget.didClampAnchor,
		OriginalTurnStartedAt:      taskBudget.originalTurnStartedAt,
		CarriedOutCalls:            request.CarriedOutCalls,
		CheckpointSender:           request.CheckpointSender,
		TaskRunChosen:              request.TaskRunChosen,
	}
	agentTurnRunner := NewAgentTurnRunnerWithRecoveryModel(
		agentKernel.taskRunService,
		agentKernel.taskStepService,
		agentKernel.taskArtifactService,
		agentKernel.taskLanguageModelForLevel(intakeDecision.TaskLevel),
		agentKernel.languageModel,
		turnOptions,
	)
	agentTurnRunner.UseIterationCostObserver(agentKernel.iterationCostObserver)
	agentTurnRunner.UseToolSelector(agentKernel.toolSelector)
	agentTurnRunner.UseToolResultSpillStore(agentKernel.toolResultSpillStore)
	agentTurnRunner.UseToolResultImageSource(agentKernel.toolResultImageSource)
	agentTurnRunner.UseDecisionModel(agentKernel.decisionModel)
	result, errorValue := agentTurnRunner.RunTurn(taskBudget.callerContext(), turnRequest)
	result.TurnRoute = turnDecision.Route
	result.ToolNames = toolNamesForEvent(turnRequest.ToolSet)
	if result.TaskRun.TaskRunID != "" {
		agentKernel.appendTurnRouterCallRecords(result.TaskRun.TaskRunID, routerCallLedger.Records)
		agentKernel.taskRunService.AppendTaskEvent(result.TaskRun.TaskRunID, agentcontract.TaskEventAgentIntake, marshalEventBody(intakeDecision))
		agentKernel.appendCompanyTimeZoneFallbackEvent(result.TaskRun.TaskRunID, turnRequest.Company)
		agentKernel.appendGoalLifecycleEvent(result.TaskRun, turnRequest.ActiveGoal)
	}
	return result, errorValue
}

func (agentKernel *AgentKernel) appendCompanyTimeZoneFallbackEvent(taskRunID string, company CompanyContext) {
	fallbackReason := agentcontract.CompanyZoneFallbackReason(company.TimeZone)
	if fallbackReason == "" {
		return
	}
	agentKernel.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAgentCompanyTimeZoneFallback, marshalEventBody(map[string]string{
		"reason":          fallbackReason,
		"companyTimeZone": company.TimeZone,
		"readTheClockIn":  companyLocation(company.TimeZone).String(),
	}))
}

func requestStartsFreshTask(turnDecision TurnDecision, request AgentRequest) bool {
	return turnDecision.Route == TurnRouteStartTask &&
		!request.IsRuntimeRestartResume &&
		strings.TrimSpace(request.ExistingTaskRunID) == ""
}

type taskLifecycleMode string

const (
	taskLifecycleFresh            taskLifecycleMode = "fresh"
	taskLifecycleRuntimeResume    taskLifecycleMode = "runtime_resume"
	taskLifecycleSemanticRevision taskLifecycleMode = "semantic_revision"
	taskLifecycleContinuation     taskLifecycleMode = "continuation"
)

func taskLifecycleModeForRequest(turnDecision TurnDecision, request AgentRequest) taskLifecycleMode {
	if request.IsRuntimeRestartResume {
		return taskLifecycleRuntimeResume
	}
	if turnDecision.Route == TurnRouteReviseTask {
		return taskLifecycleSemanticRevision
	}
	if requestStartsFreshTask(turnDecision, request) {
		return taskLifecycleFresh
	}
	return taskLifecycleContinuation
}

func pinnedToolNamesForResolvedRequest(
	manualToolNames []string,
	persistedToolNames []string,
	routerToolNames []string,
	requiredEvidenceTools []string,
	isFreshTask bool,
) []string {
	preservedToolNames := persistedToolNames
	selectedToolNames := routerToolNames
	if isFreshTask {
		preservedToolNames = manualToolNames
		selectedToolNames = appendUniqueStrings(routerToolNames, requiredEvidenceTools...)
	}
	return appendUniqueStrings(append([]string{}, preservedToolNames...), selectedToolNames...)
}

func requiredNextToolNamesForResolvedRequest(activeGoal ActiveGoal, arbitratedToolNames []string) []string {
	if len(activeGoal.RequiredNextTools) > 0 {
		return appendUniqueStrings(activeGoal.RequiredNextTools)
	}
	return appendUniqueStrings(arbitratedToolNames)
}

func (agentKernel *AgentKernel) selectInstructionBundleForResolvedRequest(ctx context.Context, baseInstructionBundle InstructionBundle, request AgentRequest, intakeDecision IntakeDecision) (InstructionBundle, IntakeDecision) {
	selectionRequest := request
	selectionContract := selectionRequest.ActiveGoal.OutcomeContract
	selectionContract.RequiredAttachmentSuffixes = appendUniqueStrings(selectionContract.RequiredAttachmentSuffixes, attachmentSuffixesForRequestedOutputFormats(intakeDecision.RequestedOutputFormats)...)
	selectionContract.ExpectedResults = appendExpectedResults(selectionContract.ExpectedResults, intakeDecision.ExpectedResults...)
	if len(selectionContract.RequiredAttachmentSuffixes) > 0 {
		selectionContract.RequiredEvidenceTools = appendUniqueStrings(selectionContract.RequiredEvidenceTools, toolcontract.FileDeliverToolName)
		selectionContract.ArtifactRequirement = ArtifactRequirementRequired
	}
	selectionRequest.ActiveGoal.OutcomeContract = normalizeOutcomeContract(selectionContract)
	instructionBundle := selectInstructionBundleForRequestWithRetrieverAndRouter(
		ctx,
		baseInstructionBundle,
		selectionRequest,
		agentKernel.skillRetriever,
		NewSkillSearchQueryRouter(agentKernel.classificationLanguageModel()),
	)
	instructionBundle = instructionBundleWithPinnedSkills(instructionBundle, selectionRequest)
	instructionBundle = instructionBundleWithToolOwningSkills(instructionBundle, selectionRequest, intakeDecision.InitialToolNames)
	return instructionBundle, intakeDecision
}

type confirmationGatePlan struct {
	ExecutionPlan    ExecutionPlan
	Decision         ConfirmationPolicyDecision
	HasExecutionPlan bool
	DegradedError    error
}

func (agentKernel *AgentKernel) planConfirmationGate(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, evidenceHints []string) (confirmationGatePlan, error) {
	if request.IsRuntimeRestartResume {
		return confirmationGatePlan{}, nil
	}
	if !shouldBuildExecutionPlanForConfirmation(request, intakeDecision, evidenceHints) {
		return confirmationGatePlan{}, nil
	}
	executionPlan, errorValue := agentKernel.BuildExecutionPlan(responseContext, request, evidenceHints)
	if errorValue != nil {
		executionPlan, errorValue = agentKernel.BuildExecutionPlan(responseContext, request, evidenceHints)
	}
	if errorValue != nil {
		return confirmationGatePlan{DegradedError: errorValue}, nil
	}
	executionPlan.OriginalInstruction = strings.TrimSpace(request.Prompt)
	decision := confirmationDecisionForIndependentWork(executionPlan, intakeDecision)
	return confirmationGatePlan{ExecutionPlan: executionPlan, Decision: decision, HasExecutionPlan: true}, nil
}

func confirmationDecisionForIndependentWork(executionPlan ExecutionPlan, intakeDecision IntakeDecision) ConfirmationPolicyDecision {
	decision := EvaluateConfirmationPolicy(executionPlan)
	if !intakeDecision.HasIndependentWork || !decision.RequiresClarification || decision.Reason != "missing_information" {
		return decision
	}
	policyPlan := executionPlan
	policyPlan.MissingInformation = nil
	policyDecision := EvaluateConfirmationPolicy(policyPlan)
	if policyDecision.RequiresClarification || policyDecision.RequiresConfirmation {
		return decision
	}
	return policyDecision
}

func (agentKernel *AgentKernel) pauseForClarification(responseContext context.Context, request AgentRequest, intakeDecision IntakeDecision, plan confirmationGatePlan, outcomeContract OutcomeContract, evidenceHints []string, selectedSkills []string) (AgentTurnResult, error) {
	executionPlan := plan.ExecutionPlan
	decision := plan.Decision
	taskRun := agentKernel.taskRunForRequest(request)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentIntake, marshalEventBody(intakeDecision))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventConfirmationPlanCreated, marshalEventBody(executionPlan))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventConfirmationPolicyDecision, marshalEventBody(decision))
	reply, errorValue := agentKernel.GenerateClarificationMessage(responseContext, request, executionPlan, decision)
	if errorValue != nil {
		return AgentTurnResult{TaskRun: taskRun}, errorValue
	}
	waitingTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusWaitingUserInput, reply)
	if errorValue != nil {
		return AgentTurnResult{}, errorValue
	}
	waitingGoal := activeGoalFromExecutionPlan(taskRun.TaskRunID, executionPlan, ActiveGoalStatusWaitingUserInput, request.ToolSet, evidenceHints, nil)
	waitingGoal.RequiredNextTools = appendUniqueStrings(request.ActiveGoal.RequiredNextTools)
	waitingGoal.OutcomeContract = normalizeOutcomeContract(outcomeContract)
	waitingGoal.SelectedToolNames = appendUniqueStrings(nil, request.PinnedToolNames...)
	waitingGoal.SelectedSkillNames = appendUniqueStrings(nil, selectedSkills...)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentGoalCreated, marshalEventBody(waitingGoal))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentGoalWaitingUserInput, marshalEventBody(waitingGoal))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventConfirmationClarificationRequested, reply)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentInputRequested, marshalEventBody(agentcontract.NewAskInputRequest(reply, nil, request.ResponseLanguage)))
	return AgentTurnResult{TaskRun: waitingTaskRun, UserNotice: reply, ToolNames: toolNamesForEvent(request.ToolSet)}, nil
}

func (agentKernel *AgentKernel) ResumeTask(taskRunID string) (agentcontract.TaskRun, error) {
	return agentKernel.taskRunService.ResumeTaskRun(taskRunID)
}

func (agentKernel *AgentKernel) taskRunForRequest(request AgentRequest) agentcontract.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentKernel.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return agentKernel.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

func (agentKernel *AgentKernel) appendTurnRouterCallRecords(taskRunID string, records []llmCallRecord) {
	for _, record := range records {
		agentKernel.taskRunService.AppendLLMCall(taskRunID, record)
	}
}

func (agentKernel *AgentKernel) appendGoalLifecycleEvent(taskRun agentcontract.TaskRun, activeGoal ActiveGoal) {
	if strings.TrimSpace(taskRun.TaskRunID) == "" {
		return
	}
	activeGoal.GoalID = firstNonEmptyString(activeGoal.GoalID, taskRun.TaskRunID)
	activeGoal.TaskRunID = firstNonEmptyString(activeGoal.TaskRunID, taskRun.TaskRunID)
	activeGoal.Status = activeGoalStatusForTaskStatus(taskRun.Status)
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, activeGoalEventNameForTaskStatus(taskRun.Status), marshalEventBody(activeGoal))
}

func (agentKernel *AgentKernel) turnOptionsForIntakeDecision(ctx context.Context, intakeDecision IntakeDecision) TurnOptions {
	baseOptions := normalizeTurnOptions(agentKernel.turnOptions)
	taskLevelProfile := TaskLevelProfileForLevel(intakeDecision.TaskLevel)
	baseOptions.TaskLevel = taskLevelProfile.TaskLevel
	baseOptions.MaxIterationCount = taskLevelProfile.MaxIterationCount
	baseOptions.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
	baseOptions.MaxElapsedSecond = int(elapsedBudgetForProfile(taskLevelProfile, agentKernel.iterationCostObserver.CostOfModelInUse()).Seconds())
	baseOptions.ElapsedBudgetSource = ElapsedBudgetFromLevel
	return withElapsedBudgetFromDeadline(ctx, baseOptions)
}

func artifactTaskLevelFloor(request AgentRequest, intakeDecision IntakeDecision) TaskLevel {
	if requestLooksLikeSlidesArtifactWork(request) || intakeDecisionRequestsVisualDeliverable(intakeDecision) {
		return TaskLevelXHigh
	}
	return TaskLevelXLow
}

func promoteArtifactTaskLevel(request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	intakeDecision.TaskLevel = LargerTaskLevel(intakeDecision.TaskLevel, artifactTaskLevelFloor(request, intakeDecision))
	return intakeDecision
}

func promoteArtifactTaskLevelForRequest(routing turnclassification.Routing, request AgentRequest, intakeDecision IntakeDecision) IntakeDecision {
	if routing.IsExact {
		return intakeDecision
	}
	return promoteArtifactTaskLevel(request, intakeDecision)
}

func (agentKernel *AgentKernel) taskLanguageModelForLevel(taskLevel TaskLevel) model.LanguageModelProvider {
	switch NormalizeTaskLevel(string(taskLevel)) {
	case TaskLevelMax:
		if agentKernel.maxTaskLanguageModel != nil {
			return agentKernel.maxTaskLanguageModel
		}
	case TaskLevelXHigh:
		if agentKernel.xHighTaskLanguageModel != nil {
			return agentKernel.xHighTaskLanguageModel
		}
	case TaskLevelHigh:
		if agentKernel.highTaskLanguageModel != nil {
			return agentKernel.highTaskLanguageModel
		}
	case TaskLevelMedium:
		if agentKernel.mediumTaskLanguageModel != nil {
			return agentKernel.mediumTaskLanguageModel
		}
	case TaskLevelLow:
		if agentKernel.lowTaskLanguageModel != nil {
			return agentKernel.lowTaskLanguageModel
		}
	case TaskLevelXLow:
		if agentKernel.xLowTaskLanguageModel != nil {
			return agentKernel.xLowTaskLanguageModel
		}
	}
	return agentKernel.languageModel
}

func (agentKernel *AgentKernel) classificationLanguageModel() model.LanguageModelProvider {
	if agentKernel.xLowTaskLanguageModel != nil {
		return agentKernel.xLowTaskLanguageModel
	}
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.languageModel
}

func (agentKernel *AgentKernel) turnRouterLanguageModel() model.LanguageModelProvider {
	if agentKernel.intakeLanguageModel != nil {
		return agentKernel.intakeLanguageModel
	}
	return agentKernel.classificationLanguageModel()
}

func restorePersistedToolSelection(request AgentRequest) AgentRequest {
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, request.ActiveGoal.SelectedToolNames...)
	request.PinnedSkillNames = appendUniqueStrings(request.PinnedSkillNames, request.ActiveGoal.SelectedSkillNames...)
	return request
}

func routedTurnDecision(routing turnclassification.Routing) (TurnDecision, error) {
	if routing.Decision == nil {
		return TurnDecision{}, errors.New("turn request carries no routing decision; the host routes before handing a turn to the harness")
	}
	return *routing.Decision, nil
}
