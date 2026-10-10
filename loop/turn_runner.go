package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/iterationcost"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type AgentTurnRunner struct {
	iterationCostObserver  *IterationCostObserver
	toolSelector           ToolSelector
	modelInUse             string
	promptTokensInUse      int64
	taskRunService         taskstate.TaskRunStore
	taskStepService        taskstate.TaskStepStore
	taskArtifactService    taskstate.TaskArtifactStore
	languageModel          model.LanguageModelProvider
	languageModelTaskLevel TaskLevel
	recoveryLanguageModel  model.LanguageModelProvider
	toolResultSpillStore   ToolResultSpillStore
	toolResultImageSource  ToolResultImageSource
	decisionModel          model.DecisionModel
	expectedChanges        *sync.Map
	options                TurnOptions
}

type TaskLevelLanguageModelResolver func(TaskLevel) model.LanguageModelProvider

type toolCallActionOutcome struct {
	Result            AgentTurnResult
	ShouldReturn      bool
	WasHandled        bool
	CanYieldToElapsed bool
}

func NewAgentTurnRunner(taskRunService taskstate.TaskRunStore, taskStepService taskstate.TaskStepStore, taskArtifactService taskstate.TaskArtifactStore, languageModel model.LanguageModelProvider, options TurnOptions) *AgentTurnRunner {
	return NewAgentTurnRunnerWithRecoveryModel(taskRunService, taskStepService, taskArtifactService, languageModel, languageModel, options)
}

func NewAgentTurnRunnerWithRecoveryModel(taskRunService taskstate.TaskRunStore, taskStepService taskstate.TaskStepStore, taskArtifactService taskstate.TaskArtifactStore, languageModel model.LanguageModelProvider, recoveryLanguageModel model.LanguageModelProvider, options TurnOptions) *AgentTurnRunner {
	if taskArtifactService == nil {
		taskArtifactService = taskstate.NewTaskArtifactService()
	}
	if recoveryLanguageModel == nil {
		recoveryLanguageModel = languageModel
	}
	normalizedOptions := normalizeTurnOptions(options)
	return &AgentTurnRunner{
		iterationCostObserver:  NewIterationCostObserver(),
		taskRunService:         taskRunService,
		taskStepService:        taskStepService,
		taskArtifactService:    taskArtifactService,
		languageModel:          languageModel,
		languageModelTaskLevel: normalizedOptions.TaskLevel,
		recoveryLanguageModel:  recoveryLanguageModel,
		options:                normalizedOptions,
		expectedChanges:        &sync.Map{},
	}
}

func (agentTurnRunner *AgentTurnRunner) UseToolSelector(toolSelector ToolSelector) {
	agentTurnRunner.toolSelector = toolSelector
}

func (agentTurnRunner *AgentTurnRunner) UseToolResultSpillStore(toolResultSpillStore ToolResultSpillStore) {
	agentTurnRunner.toolResultSpillStore = toolResultSpillStore
}

func (agentTurnRunner *AgentTurnRunner) UseToolResultImageSource(toolResultImageSource ToolResultImageSource) {
	agentTurnRunner.toolResultImageSource = toolResultImageSource
}

func (agentTurnRunner *AgentTurnRunner) UseDecisionModel(decisionModel model.DecisionModel) {
	agentTurnRunner.decisionModel = decisionModel
}

func (agentTurnRunner *AgentTurnRunner) llmCallObserverForTaskRun(taskRunID string) llmCallObserver {
	return func(record llmCallRecord) {
		agentTurnRunner.taskRunService.AppendLLMCall(taskRunID, record)
		agentTurnRunner.noteModelInUse(record.Model)
		agentTurnRunner.noteContextInUse(record.PromptTokens)
	}
}

func (agentTurnRunner *AgentTurnRunner) appendCallRecords(taskRunID string, records []llmCallRecord) {
	for _, record := range records {
		agentTurnRunner.taskRunService.AppendLLMCall(taskRunID, record)
	}
}

func (agentTurnRunner *AgentTurnRunner) UseIterationCostObserver(observer *IterationCostObserver) {
	if observer == nil {
		return
	}
	agentTurnRunner.iterationCostObserver = observer
}

func (agentTurnRunner *AgentTurnRunner) noteModelInUse(modelName string) {
	if strings.TrimSpace(modelName) == "" {
		return
	}
	agentTurnRunner.modelInUse = modelName
}

func (agentTurnRunner *AgentTurnRunner) noteContextInUse(promptTokens int64) {
	if promptTokens <= 0 {
		return
	}
	agentTurnRunner.promptTokensInUse = promptTokens
}

func (agentTurnRunner *AgentTurnRunner) toolResultLimit() int {
	conversationBudgetTokens := compactionTriggerTokenThreshold(agentTurnRunner.options.ContextWindowTokens)
	shareOfOneObservation := conversationBudgetTokens * charactersPerToken / maxProgressObservations
	if agentTurnRunner.options.ContextWindowTokens <= 0 {
		return max(shareOfOneObservation, maxSummaryTextLength)
	}
	remainingCharacters := (int64(agentTurnRunner.options.ContextWindowTokens) - agentTurnRunner.promptTokensInUse) * charactersPerToken
	return max(min(int(remainingCharacters), shareOfOneObservation), maxSummaryTextLength)
}

func (agentTurnRunner *AgentTurnRunner) recordIterationCost(startedAt time.Time) {
	agentTurnRunner.iterationCostObserver.Record(agentTurnRunner.modelInUse, time.Since(startedAt))
}

func normalizeTurnOptions(options TurnOptions) TurnOptions {
	taskLevelProfile := TaskLevelProfileForLevel(options.TaskLevel)
	if options.TaskLevel == "" {
		options.TaskLevel = taskLevelProfile.TaskLevel
	}
	if options.MaxIterationCount <= 0 {
		options.MaxIterationCount = taskLevelProfile.MaxIterationCount
	}
	if options.MaxToolCallCount < 0 {
		options.MaxToolCallCount = 0
	}
	if options.MaxToolCallCount == 0 {
		options.MaxToolCallCount = taskLevelProfile.MaxToolCallCount
	}
	if options.MaxElapsedSecond <= 0 {
		options.MaxElapsedSecond = int(taskLevelProfile.Duration.Seconds())
		options.ElapsedBudgetSource = ElapsedBudgetFromLevel
	} else if options.ElapsedBudgetSource == "" {
		options.ElapsedBudgetSource = ElapsedBudgetFromCaller
	}
	if recoveryBudgetIsUnset(options.RecoveryBudget) {
		options.RecoveryBudget = defaultRecoveryBudget()
	} else {
		options.RecoveryBudget = normalizeRecoveryBudget(options.RecoveryBudget)
	}
	if options.RecoveryAttemptLimit <= 0 {
		options.RecoveryAttemptLimit = recoveryToolBudgetTotal(options.RecoveryBudget)
	}
	return options
}

func requestReducedToCallableTools(request AgentTurnRequest) AgentTurnRequest {
	request.OutcomeContract = contractReducedToCallableTools(request.ToolSet, request.OutcomeContract)
	request.ActiveGoal.OutcomeContract = contractReducedToCallableTools(request.ToolSet, request.ActiveGoal.OutcomeContract)
	request.RequiredEvidenceTools = callableToolNames(request.ToolSet, request.RequiredEvidenceTools)
	return request
}

func (agentTurnRunner *AgentTurnRunner) RunTurn(ctx context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	if agentTurnRunner.languageModel == nil {
		return AgentTurnResult{}, errors.New("language model provider is not configured")
	}
	request = requestReducedToCallableTools(request)

	turnContext := ctx
	turnContext = model.ContextWithRequestContext(turnContext, model.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	})
	if request.TurnStartedAt.IsZero() {
		request.TurnStartedAt = time.Now().Add(-2 * time.Second)
	}
	if request.EffortStartedAt.IsZero() {
		request.EffortStartedAt = time.Now()
	}
	request.ResponseLanguage = ResolveResponseLanguage(request.ResponseLanguage)
	request, _ = applyToolRequest(request, requestToolsArguments{
		ToolNames:  request.PinnedToolNames,
		SkillNames: request.PinnedSkillNames,
	})

	taskRun := agentTurnRunner.taskRunForRequest(request)
	if request.TaskRunChosen != nil {
		request.TaskRunChosen(taskRun.TaskRunID)
	}
	isPausedTaskResume := taskRun.Status == agentcontract.TaskStatusWaitingApproval || taskRun.Status == agentcontract.TaskStatusWaitingUserInput
	if request.TurnAnchorClamped {
		agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentTurnAnchorClamped, marshalEventBody(map[string]any{
			"phase":                       "execution",
			"originalTurnStartedAtUnixMs": request.OriginalTurnStartedAt.UnixMilli(),
			"clampedTurnStartedAtUnixMs":  request.TurnStartedAt.UnixMilli(),
			"nowUnixMs":                   time.Now().UnixMilli(),
		}))
	}
	agentTurnRunner.appendTaskSourceEvent(taskRun.TaskRunID, request.SourceReference)
	agentTurnRunner.appendConversationBudgetEvent(taskRun.TaskRunID, agentTurnRunner.options.ContextWindowTokens)
	observeRecord := agentTurnRunner.llmCallObserverForTaskRun(taskRun.TaskRunID)
	agentTurnRunner.languageModel = observeLanguageModel(agentTurnRunner.languageModel, observeRecord)
	if agentTurnRunner.recoveryLanguageModel == nil {
		agentTurnRunner.recoveryLanguageModel = agentTurnRunner.languageModel
	} else {
		agentTurnRunner.recoveryLanguageModel = observeLanguageModel(agentTurnRunner.recoveryLanguageModel, observeRecord)
	}
	turnContext = agentcontract.WithLLMCallObserver(turnContext, observeRecord)
	taskContext, taskCancel := context.WithCancel(turnContext)
	defer taskCancel()
	agentTurnRunner.beginExpectedChanges(taskContext, taskRun.TaskRunID, request)
	defer agentTurnRunner.forgetExpectedChanges(taskRun.TaskRunID)
	unregisterTaskCancel := agentTurnRunner.taskRunService.RegisterTaskRunCancel(taskRun.TaskRunID, taskCancel)
	defer unregisterTaskCancel()
	runningTaskRun, errorValue := agentTurnRunner.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant")
	if errorValue != nil {
		return agentTurnRunner.failLaunchStep(turnContext, taskRun, request, "start_attempt", errorValue), nil
	}
	taskRun = runningTaskRun
	workContext, cancelWork := agentTurnRunner.currentEffortContext(taskContext, request.EffortStartedAt)
	defer func() {
		cancelWork()
	}()
	refreshWorkContext := func() {
		cancelWork()
		workContext, cancelWork = agentTurnRunner.currentEffortContext(taskContext, request.EffortStartedAt)
	}
	agentTurnRunner.appendInstructionEvent(taskRun.TaskRunID, request)

	state, errorValue := agentTaskStateForTurn(request, agentTurnRunner.options, taskRun, agentTurnRunner.taskRunService.ListTaskEvent(taskRun.TaskRunID), isPausedTaskResume)
	if errorValue != nil {
		return agentTurnRunner.failLaunchStep(turnContext, taskRun, request, "restore_state", errorValue), nil
	}
	agentTurnRunner.bringBackImagesTheTurnAlreadyRead(workContext, taskRun.TaskRunID, state.Observations)
	successfulToolCalls := map[string]turnObservation{}
	agentTurnRunner.recordCarriedOutCalls(workContext, taskRun.TaskRunID, request, &state, successfulToolCalls)
	limitPressureWarnings := map[string]bool{}
	warningsRetiredByGrant := false
	progressTracker := newActionProgressTracker(state.Observations)
	appliedSteerEventIDs := appliedSteerEventIDsFromTaskEvents(agentTurnRunner.taskRunService.ListTaskEvent(taskRun.TaskRunID))
	noProgressStopEvaluation := func() (actionProgressEvaluation, bool) {
		progressEvaluation := progressTracker.evaluate(state.Observations)
		if progressEvaluation.HasProgress {
			return progressEvaluation, false
		}
		return progressEvaluation, progressEvaluation.shouldStop()
	}
	stopForNoProgress := func(stepID string) (AgentTurnResult, bool) {
		progressEvaluation, shouldStop := noProgressStopEvaluation()
		if !shouldStop {
			return AgentTurnResult{}, false
		}
		recoveryAllowance := evaluateRecoveryAllowance(state.Observations, agentTurnRunner.options.RecoveryBudget)
		if agentTurnRunner.continueStalledRecoveryIfAllowed(taskRun.TaskRunID, &state, &progressTracker, recoveryAllowance) {
			return AgentTurnResult{}, false
		}
		if agentTurnRunner.steerStalledTurnTowardNextTool(taskRun.TaskRunID, &state, &progressTracker) {
			return AgentTurnResult{}, false
		}
		if agentTurnRunner.steerStalledTurnTowardExit(taskRun.TaskRunID, &state, &progressTracker) {
			return AgentTurnResult{}, false
		}
		reason := "stopped after repeated model actions without workspace, tool, artifact, attachment, or new failure progress, including after stall guidance"
		if agentTurnRunner.shouldPauseForStalledRecovery(taskRun.TaskRunID, state.Observations) {
			if result, isPaused := agentTurnRunner.pauseTurnForStall(workContext, taskRun.TaskRunID, stepID, request, reason, progressEvaluation, recoveryAllowance, state); isPaused {
				return result, true
			}
		}
		result, isBlocked := agentTurnRunner.blockTurnForStall(workContext, taskRun.TaskRunID, stepID, request, reason, progressEvaluation, recoveryAllowance, state)
		return result, isBlocked
	}
	iterationStartedAt := time.Now()
	iterationSpentModelCall := false
	for iteration := 1; ; iteration++ {
		if iteration > 1 {
			if iterationSpentModelCall {
				agentTurnRunner.recordIterationCost(iterationStartedAt)
				budgetBeforeRefresh := agentTurnRunner.options.MaxElapsedSecond
				agentTurnRunner.refreshElapsedBudget(state.budgetTaskLevel())
				if agentTurnRunner.options.MaxElapsedSecond > budgetBeforeRefresh {
					refreshWorkContext()
				}
			}
			iterationStartedAt = time.Now()
			iterationSpentModelCall = false
		}
		if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
			return cancelledResult, nil
		}
		if ctx.Err() != nil {
			return agentTurnRunner.abandonedTurnResult(taskContext, taskRun.TaskRunID, request, ctx.Err(), "the turn's caller context ended before the agent could act: "+errorString(ctx.Err()), state.Attachments), nil
		}
		if result, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration-1); isElapsed {
			return result, errorValue
		}
		if iteration > agentTurnRunner.options.MaxIterationCount && !agentTurnRunner.extendBudgetOneLevelOnce(taskRun.TaskRunID, &state) {
			result, errorValue := agentTurnRunner.completeOrStopForLimit(workContext, taskRun.TaskRunID, request, "max_iterations", &state, iteration-1)
			if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
				if elapsedResult, isElapsed, elapsedError := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration-1); isElapsed {
					return elapsedResult, elapsedError
				}
			}
			return result, errorValue
		}
		state.Observations = agentTurnRunner.applyPendingSteeringEvents(taskRun.TaskRunID, state.Observations, appliedSteerEventIDs)
		state.IterationCount = iteration - 1
		if state.didExtendBudgetOneLevel() && !warningsRetiredByGrant {
			warningsRetiredByGrant = true
			refreshWorkContext()
			limitPressureWarnings = map[string]bool{}
			grantedBudget := grantedBudgetObservation(state.Observations, agentTurnRunner.options)
			state.Observations = append(state.Observations, grantedBudget)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentBudgetUpdateSent, marshalEventBody(grantedBudget))
		}
		if warning := agentTurnRunner.nextLimitPressureWarning(state, iteration-1, state.ToolCallCount, agentTurnRunner.turnElapsed(taskContext, request.EffortStartedAt), len(state.Observations)+1, limitPressureWarnings); warning != nil {
			if warning.Observation != nil {
				state.Observations = append(state.Observations, *warning.Observation)
			}
			dropPendingBatchedActions(&state, batchStoppedByLimitPressure)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLimitPressure, marshalEventBody(warning.EventBody))
			limitPressureWarnings[warning.Stage] = true
		}
		stepID := fmt.Sprintf("%s:turn-%03d", taskRun.TaskRunID, iteration)
		agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusRunning, "agent turn iteration", "")

		if workContext.Err() != nil {
			return agentTurnRunner.abandonedTurnResult(taskContext, taskRun.TaskRunID, request, workContext.Err(), "the turn's work context ended before the agent could act: "+errorString(workContext.Err()), state.Attachments), nil
		}
		iterationRequest := agentTurnRunner.requestForStep(workContext, request, &state)
		state.ShouldRestrictNextActionToTerminal = false
		agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentStepWorkingSet, marshalEventBody(map[string]any{
			"step":     iteration,
			"exposure": iterationRequest.ToolExposure,
		}))
		allowQualityCriteria := len(state.QualityCriteria) == 0 && outcomeContractNeedsQualityCriteria(iterationRequest.OutcomeContract)
		actionDocument, isBatched := takeBatchedAction(&state)
		iterationSpentModelCall = !isBatched
		var actionError error
		if !isBatched {
			actionDocument, actionError = agentTurnRunner.nextAction(workContext, taskRun.TaskRunID, iterationRequest, state, allowQualityCriteria)
		}
		if actionError != nil && isUnreadableModelActionError(actionError) {
			state.Observations = append(state.Observations, unreadableActionObservation(state.Observations, actionError))
			agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentUnreadableAction, marshalEventBody(map[string]string{"reason": actionError.Error()}))
			continue
		}
		if actionError != nil {
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusFailed, "agent turn iteration", actionError.Error())
			if errors.Is(actionError, context.Canceled) {
				return agentTurnRunner.abandonedTurnResult(taskContext, taskRun.TaskRunID, request, actionError, "the model call was cancelled: "+actionError.Error(), state.Attachments), nil
			}
			if errors.Is(actionError, context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return agentTurnRunner.abandonedTurnResult(taskContext, taskRun.TaskRunID, request, ctx.Err(), "the turn's caller context ended while the model was answering: "+actionError.Error(), state.Attachments), nil
				}
				if !agentTurnRunner.currentEffortElapsed(taskContext, request.EffortStartedAt) {
					refreshWorkContext()
					continue
				}
				if agentTurnRunner.options.ElapsedBudgetSource != ElapsedBudgetFromCaller && agentTurnRunner.extendBudgetOneLevelOnce(taskRun.TaskRunID, &state) {
					refreshWorkContext()
					continue
				}
				return agentTurnRunner.stopAtElapsedLimit(taskContext, taskRun.TaskRunID, request, &state, iteration-1)
			}
			return agentTurnRunner.finalizeIfSatisfiedOrFail(taskContext, request, "llm action failed: "+actionError.Error(), &state, iteration)
		}

		if message := strings.TrimSpace(actionDocument.Message); message != "" {
			state.LastModelMessage = message
		}

		if !executionStateIsEmpty(actionDocument.ExecutionStateUpdate) {
			state.ExecutionState = normalizeExecutionState(actionDocument.ExecutionStateUpdate)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentExecutionState, marshalEventBody(state.ExecutionState))
		}
		agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentAction, marshalEventBody(actionDocument))
		switch strings.TrimSpace(actionDocument.Action) {
		case "set_quality_criteria":
			state.QualityCriteria = normalizeQualityCriteria(actionDocument.QualityCriteria)
			observation := turnObservation{
				ObservationID: nextObservationIDForObservations(state.Observations),
				Action:        "set_quality_criteria",
				Output:        toolcontract.ToolOutput{Content: marshalEventBody(map[string]any{"criteria": state.QualityCriteria})},
			}
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentQualityCriteria, marshalEventBody(map[string]any{
				"criteria": state.QualityCriteria,
			}))
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "set_quality_criteria", marshalEventBody(map[string]any{"criteria": state.QualityCriteria}))
			continue
		case "delegate":
			observation := agentTurnRunner.runDelegatedTurn(workContext, taskRun.TaskRunID, &state, actionDocument)
			agentTurnRunner.recordToolObservation(taskRun.TaskRunID, &state, actionDocument, successfulToolCalls, observation, "")
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "delegate", observation.ContentText())
			continue
		case "reply":
			if result, shouldReturn := agentTurnRunner.handleReplyAction(workContext, taskRun.TaskRunID, stepID, iterationRequest, &state, successfulToolCalls, actionDocument); shouldReturn {
				return result, nil
			}
			if result, shouldStop := stopForNoProgress(stepID); shouldStop {
				if elapsedResult, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
					return elapsedResult, errorValue
				}
				return result, nil
			}
			continue
		case "finish":
			deliveredDocument, isDelivered := agentTurnRunner.deliverFinishAttachments(workContext, taskRun.TaskRunID, iterationRequest, &state, successfulToolCalls, actionDocument)
			if !isDelivered {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusFailed, "reply", lastObservationText(state.Observations))
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					if elapsedResult, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
						return elapsedResult, errorValue
					}
					return result, nil
				}
				continue
			}
			actionDocument = deliveredDocument
			completionGateResult := agentTurnRunner.validateCompletionGateWithChanges(workContext, taskRun.TaskRunID, request, state.Observations, actionDocument)
			agentTurnRunner.appendValidityReview(taskRun.TaskRunID, "finish", completionGateResult.ValidityState)
			if !completionGateResult.IsSatisfied {
				if candidateReply := finishActionMessage(actionDocument); canDeliverBestEffortOnUnmetChanges(workContext, completionGateResult, candidateReply) {
					agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentCompletionStateBestEffort, marshalEventBody(map[string]string{"reason": completionGateResult.Message}))
					result := agentTurnRunner.completeTaskRunBestEffort(workContext, taskRun.TaskRunID, stepID, "finish", request, state.Observations, completionGateResult, candidateReply)
					return result, nil
				}
				observation := completionGateObservation(len(state.Observations)+1, completionGateResult, state.Request.ToolSet, state.Observations)
				observation = withCompletionGateRecoveryPacket(observation, completionGateResult)
				state.Observations = append(state.Observations, observation)
				agentTurnRunner.appendEvent(taskRun.TaskRunID, completionGateEventName(observation), marshalEventBody(observation))
				if observation.Action == "evidence_missing" {
					agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentCompletionRequired, marshalEventBody(observation))
				}
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, observation.Action, observation.ContentText())
				if result, shouldStop := stopForNoProgress(stepID); shouldStop {
					if elapsedResult, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
						return elapsedResult, errorValue
					}
					return result, nil
				}
				continue
			}
			agentTurnRunner.appendQualityReview(taskRun.TaskRunID, state.QualityCriteria, actionDocument.QualityReview, state.Observations)
			reply := finishActionMessage(actionDocument)
			if reply == "" {
				agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusFailed, "finish", "empty final reply message")
				return agentTurnRunner.finalizeIfSatisfiedOrFail(taskContext, request, "empty final reply message", &state, iteration)
			}
			reply, carried := agentTurnRunner.replyForFinish(workContext, taskRun.TaskRunID, request, &state, completionGateResult, reply)
			reply = agentTurnRunner.prepareFinishMessageForPlatform(workContext, request, reply)
			if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRun.TaskRunID, state.Attachments); isCancelled {
				return cancelledResult, nil
			}
			if result, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
				return result, errorValue
			}
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "finish", reply)
			result := agentTurnRunner.finishedTurnResult(taskRun.TaskRunID, reply, carried)
			result.RecoveryActions = recoveryActionsFromObservations(state.Observations)
			return result, nil
		case "continue":
			outcome := agentTurnRunner.handleToolCallAction(workContext, taskContext, taskRun.TaskRunID, stepID, iteration, iterationRequest, &state, actionDocument, successfulToolCalls, stopForNoProgress)
			if outcome.ShouldReturn {
				if outcome.CanYieldToElapsed {
					if result, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
						return result, errorValue
					}
				}
				return outcome.Result, nil
			}
			rememberBatchedActions(&state, actionDocument, iterationRequest.ToolSet.ListToolNames(), iterationRequest.ToolExposure)
			if outcome.WasHandled {
				continue
			}
		case "fail":
			if _, hasFailureDebt := activeFailureDebt(state.Observations); hasFailureDebt {
				facts := buildFailureReportFacts(state.Observations, agentTurnRunner.options.RecoveryBudget)
				failureReportResult := validateFailureReportAction(actionDocument, facts)
				if !failureReportResult.IsSatisfied {
					observation := completionGateObservation(len(state.Observations)+1, failureReportResult, state.Request.ToolSet, state.Observations)
					state.Observations = append(state.Observations, observation)
					agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReportRejected, marshalEventBody(observation))
					agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "failure_report_rejected", observation.ContentText())
					if result, shouldStop := stopForNoProgress(stepID); shouldStop {
						if elapsedResult, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
							return elapsedResult, errorValue
						}
						return result, nil
					}
					continue
				}
				if strings.TrimSpace(actionDocument.Message) != "" {
					if result, handled, _ := agentTurnRunner.failTerminalNoToolsFailure(taskRun.TaskRunID, stepID, request, &state, actionDocument); handled {
						return result, nil
					}
				}
				agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReportFactsUsed, marshalEventBody(actionDocument.UsedFailureFacts))
			}
			reason := firstNonEmptyString(actionDocument.Reason, "agent reported failure")
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusFailed, "fail", reason)
			return agentTurnRunner.finalizeIfSatisfiedOrFail(taskContext, request, reason, &state, iteration)
		default:
			observation := newFailureObservation(nextObservationIDForObservations(state.Observations), "invalid_action", "", "unknown action: "+actionDocument.Action, toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "action_parse")
			state.Observations = append(state.Observations, observation)
			agentTurnRunner.saveStep(taskRun.TaskRunID, stepID, agentcontract.TaskStatusCompleted, "invalid_action", observation.ContentText())
			if result, shouldStop := stopForNoProgress(stepID); shouldStop {
				if elapsedResult, isElapsed, errorValue := agentTurnRunner.stopForElapsedLimitIfReached(taskContext, taskRun.TaskRunID, request, &state, iteration); isElapsed {
					return elapsedResult, errorValue
				}
				return result, nil
			}
		}
	}
}

func (agentTurnRunner *AgentTurnRunner) handleToolCallAction(ctx context.Context, taskContext context.Context, taskRunID string, stepID string, iteration int, request AgentTurnRequest, state *agentTaskState, actionDocument turnActionDocument, successfulToolCalls map[string]turnObservation, stopForNoProgress func(string) (AgentTurnResult, bool)) toolCallActionOutcome {
	effortContext, cancelEffort := agentTurnRunner.currentEffortContext(ctx, request.EffortStartedAt)
	defer cancelEffort()
	invocationContext, cancelInvocation := agentTurnRunner.toolInvocationContext(taskContext, effortContext, request, actionDocument.ToolName)
	defer cancelInvocation()
	if outcome := agentTurnRunner.rejectMalformedToolCall(taskRunID, stepID, request, state, actionDocument, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	if outcome := agentTurnRunner.rejectRepeatedToolCall(taskRunID, stepID, state, actionDocument, successfulToolCalls, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	recoveryStep, outcome := agentTurnRunner.prepareRecoveryAttempt(ctx, taskRunID, stepID, request, state, actionDocument, stopForNoProgress)
	if outcome.WasHandled {
		return outcome
	}
	if outcome := agentTurnRunner.rejectUnavailableToolCall(taskRunID, stepID, request, state, actionDocument, stopForNoProgress); outcome.WasHandled {
		return outcome
	}
	agentTurnRunner.notePlanMissingBeforeStateChange(taskRunID, request, state, actionDocument)
	state.ToolCallCount++
	if state.ToolCallCount > maxToolCallCountWithRecovery(agentTurnRunner.options, state.Observations) && !agentTurnRunner.extendBudgetOneLevelOnce(taskRunID, state) {
		result, _ := agentTurnRunner.completeOrStopForLimit(ctx, taskRunID, request, "max_tool_calls", state, iteration)
		agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusBlocked, "limit stop", "max_tool_calls")
		return toolCallActionOutcome{Result: result, ShouldReturn: true, WasHandled: true, CanYieldToElapsed: result.TaskRun.Status != agentcontract.TaskStatusCompleted}
	}
	state.Observations = agentTurnRunner.sendCheckpointMessage(effortContext, taskRunID, request, actionDocument, state.Observations)
	if strings.TrimSpace(actionDocument.Message) != "" {
		state.LastModelMessage = ""
	}
	observationID := nextObservationIDForObservations(state.Observations)
	observation := agentTurnRunner.invokeTool(invocationContext, request.ToolSet, taskRunID, observationID, actionDocument.ToolName, actionDocument.ToolInput, request.WorkspaceRootPath, request.TurnStartedAt, request.ResponseLanguage, actionDocument.Message, actionDocument.AssistantText, actionDocument.ModelReasoning, actionDocument.ModelReasoningField)
	if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, state.Attachments); isCancelled {
		return toolCallActionOutcome{Result: cancelledResult, ShouldReturn: true, WasHandled: true}
	}
	if isApprovalRequiredObservation(observation) {
		if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
			agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "approval "+actionDocument.ToolName, observation.ContentText())
			return toolCallActionOutcome{Result: pausedResult, ShouldReturn: true, WasHandled: true}
		}
	}
	agentTurnRunner.recordToolObservation(taskRunID, state, actionDocument, successfulToolCalls, observation, recoveryStep)
	agentTurnRunner.applyPlanObservation(effortContext, taskRunID, state, observation)
	updateCompletionIntent(state, actionDocument, observation)
	if pausedResult, isPaused := agentTurnRunner.pausedTaskResult(taskRunID, observation, state.Attachments); isPaused {
		agentTurnRunner.saveStep(taskRunID, stepID, pausedResult.TaskRun.Status, "continue "+actionDocument.ToolName, observation.ContentText())
		return toolCallActionOutcome{Result: pausedResult, ShouldReturn: true, WasHandled: true}
	}
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusCompleted, "continue "+actionDocument.ToolName, observation.ContentText())
	if !observation.Failed() && observation.RepeatsObservationID != "" && hasPendingObservedSuggestedNextTool(state.Observations) {
		result, shouldStop := stopForNoProgress(stepID)
		return noProgressToolCallActionOutcome(result, shouldStop)
	}
	return toolCallActionOutcome{WasHandled: true}
}

func noProgressToolCallActionOutcome(result AgentTurnResult, shouldStop bool) toolCallActionOutcome {
	return toolCallActionOutcome{
		Result:            result,
		ShouldReturn:      shouldStop,
		WasHandled:        true,
		CanYieldToElapsed: shouldStop,
	}
}

func updateCompletionIntent(state *agentTaskState, actionDocument turnActionDocument, observation turnObservation) {
	state.CompletionIntentToolName = ""
	if observation.Failed() || actionDocument.GoalSatisfied == nil || !*actionDocument.GoalSatisfied || actionDocument.HasRemainingWork {
		return
	}
	state.CompletionIntentToolName = observation.Tool
}

func (agentTurnRunner *AgentTurnRunner) applyPendingSteeringEvents(taskRunID string, observations []turnObservation, appliedEventIDs map[string]bool) []turnObservation {
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name != agentcontract.TaskEventAgentSteerReceived || appliedEventIDs[taskEvent.TaskEventID] {
			continue
		}
		var document struct {
			MessageID   string `json:"messageID"`
			Instruction string `json:"instruction"`
			Reason      string `json:"reason"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) != nil {
			continue
		}
		instruction := strings.TrimSpace(document.Instruction)
		if instruction == "" {
			continue
		}
		observation := newContentObservation(nextObservationIDForObservations(observations), "steer", "", "This is the latest user correction for the current task; update the plan before continuing.\n"+marshalEventBody(map[string]string{
			"instruction": instruction,
			"reason":      strings.TrimSpace(document.Reason),
			"messageID":   strings.TrimSpace(document.MessageID),
		}))
		observation.Summary = "User steering instruction: " + instruction
		observations = append(observations, observation)
		appliedEventIDs[taskEvent.TaskEventID] = true
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventTaskSteerApplied, marshalEventBody(map[string]string{
			"sourceEventID": taskEvent.TaskEventID,
			"observationID": observation.ObservationID,
			"messageID":     strings.TrimSpace(document.MessageID),
		}))
	}
	return observations
}

func appliedSteerEventIDsFromTaskEvents(taskEvents []agentcontract.TaskEvent) map[string]bool {
	eventIDs := map[string]bool{}
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.TaskEventTaskSteerApplied {
			continue
		}
		var document struct {
			SourceEventID string `json:"sourceEventID"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) == nil && strings.TrimSpace(document.SourceEventID) != "" {
			eventIDs[strings.TrimSpace(document.SourceEventID)] = true
		}
	}
	return eventIDs
}

func (agentTurnRunner *AgentTurnRunner) taskRunForRequest(request AgentTurnRequest) agentcontract.TaskRun {
	if taskRunID := strings.TrimSpace(request.ExistingTaskRunID); taskRunID != "" {
		if taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID); isFound {
			return taskRun
		}
	}
	return agentTurnRunner.taskRunService.CreateTaskRunWithOrigin(request.RequesterPersonID, taskstate.TaskRunOrigin{
		ConversationID: request.ConversationID,
		ReplyTargetID:  request.OriginReplyTargetID,
		IsThread:       request.OriginIsThread,
	}, request.Prompt)
}

// The window comes from the endpoint, and one that will not name it leaves every fitting decision
// derived from a default an order of magnitude smaller.
func (agentTurnRunner *AgentTurnRunner) appendConversationBudgetEvent(taskRunID string, contextWindowTokens int) {
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentConversationBudget, marshalEventBody(map[string]any{
		"contextWindowTokens":        contextWindowTokens,
		"windowWasDeclared":          contextWindowTokens > 0,
		"compactionTriggerTokens":    compactionTriggerTokenThreshold(contextWindowTokens),
		"observationShareCharacters": compactionTriggerTokenThreshold(contextWindowTokens) * charactersPerToken / maxProgressObservations,
	}))
}

func (agentTurnRunner *AgentTurnRunner) appendTaskSourceEvent(taskRunID string, sourceReference string) {
	trimmedSourceReference := strings.TrimSpace(sourceReference)
	if trimmedSourceReference == "" {
		return
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentTaskSource, marshalEventBody(map[string]string{
		"sourceReference": trimmedSourceReference,
	}))
}

func (agentTurnRunner *AgentTurnRunner) nextAction(ctx context.Context, taskRunID string, iterationRequest AgentTurnRequest, state agentTaskState, allowQualityCriteria bool) (turnActionDocument, error) {
	actionState := agentTurnRunner.actionStateForIteration(iterationRequest, state, allowQualityCriteria)
	actionState = agentTurnRunner.promptStateForAction(ctx, taskRunID, actionState)
	return agentTurnRunner.decideActionPatiently(ctx, taskRunID, actionState)
}

func (agentTurnRunner *AgentTurnRunner) actionStateForIteration(iterationRequest AgentTurnRequest, state agentTaskState, allowQualityCriteria bool) agentTaskState {
	return agentTaskState{
		Request:           iterationRequest,
		Options:           agentTurnRunner.options,
		Observations:      append([]turnObservation{}, state.Observations...),
		ExecutionState:    state.ExecutionState,
		ContextSummary:    state.ContextSummary,
		QualityCriteria:   qualityCriteriaForActionRequest(allowQualityCriteria),
		SystemInstruction: state.SystemInstruction,
	}
}

func (agentTurnRunner *AgentTurnRunner) decideActionPatiently(ctx context.Context, taskRunID string, state agentTaskState) (turnActionDocument, error) {
	patience, isMeasured := iterationcost.ModelCallPatience(agentTurnRunner.iterationCostObserver.CostOfModelInUse())
	if !isMeasured {
		return DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
	}
	callContext, cancelCall := context.WithTimeout(ctx, patience)
	actionDocument, errorValue := DecideAgentAction(callContext, agentTurnRunner.languageModel, state)
	wasCut := errors.Is(callContext.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancelCall()
	if errorValue == nil || !wasCut {
		return actionDocument, errorValue
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentModelCallCut, marshalEventBody(map[string]any{
		"model":           agentTurnRunner.modelInUse,
		"patienceSeconds": int(patience.Seconds()),
	}))
	return DecideAgentAction(ctx, agentTurnRunner.languageModel, state)
}

func outcomeContractNeedsQualityCriteria(contract OutcomeContract) bool {
	artifactRequirement := strings.TrimSpace(contract.ArtifactRequirement)
	if artifactRequirement != "" && artifactRequirement != ArtifactRequirementNone {
		return true
	}
	if len(contract.RequiredAttachmentSuffixes) > 0 {
		return true
	}
	return expectedResultIncludesType(contract, ExpectedResultTypeFile) ||
		expectedResultIncludesType(contract, ExpectedResultTypeLink)
}

func (agentTurnRunner *AgentTurnRunner) saveStep(taskRunID string, taskStepID string, status agentcontract.TaskStatus, instruction string, output string) {
	agentTurnRunner.taskStepService.AddTaskStep(taskstate.TaskStep{
		TaskStepID:               taskStepID,
		TaskRunID:                taskRunID,
		AssignedAgentProfileName: "assistant",
		Instruction:              instruction,
		Status:                   status,
		Output:                   output,
	})
}

func (agentTurnRunner *AgentTurnRunner) appendEvent(taskRunID string, name string, body string) {
	agentTurnRunner.taskRunService.AppendTaskEvent(taskRunID, name, body)
}

func (agentTurnRunner *AgentTurnRunner) appendValidityReview(taskRunID string, phase string, validityState ValidityState) {
	if len(validityState.CheckedArtifacts) == 0 {
		return
	}
	body := map[string]any{
		"phase":            phase,
		"passed":           validityState.Passed,
		"checkedArtifacts": validityState.CheckedArtifacts,
		"invalidArtifacts": validityState.InvalidArtifacts,
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentValidityReview, marshalEventBody(body))
}

func (agentTurnRunner *AgentTurnRunner) appendQualityReview(taskRunID string, criteria []qualityCriterion, review []qualityReviewItem, observations []turnObservation) {
	if len(criteria) == 0 {
		return
	}
	qualityState := buildQualityState(criteria, review, observations)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentQualityReview, marshalEventBody(qualityState))
}

func (agentTurnRunner *AgentTurnRunner) toolInvocationContext(taskContext context.Context, effortContext context.Context, request AgentTurnRequest, toolName string) (context.Context, context.CancelFunc) {
	if !toolChangesSomething(request.ToolSet, toolName) {
		return context.WithCancel(effortContext)
	}
	return agentTurnRunner.elapsedClosingContext(taskContext, request.EffortStartedAt)
}

func toolChangesSomething(toolSet *toolcontract.ToolSet, toolName string) bool {
	if toolSet == nil {
		return false
	}
	toolDefinition, isKnown := toolSet.ToolDefinition(strings.TrimSpace(toolName))
	return isKnown && ToolDefinitionRequiresSideEffectEvidence(toolDefinition)
}

func marshalEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(document)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func (agentTurnRunner *AgentTurnRunner) recordCarriedOutCalls(ctx context.Context, taskRunID string, request AgentTurnRequest, state *agentTaskState, successfulToolCalls map[string]turnObservation) {
	holds := agentTurnRunner.unspentHolds(taskRunID)
	for _, carriedOutCall := range request.CarriedOutCalls {
		toolName := strings.TrimSpace(carriedOutCall.ToolName)
		if toolName == "" {
			continue
		}
		didDriftFromItsHold := agentTurnRunner.noteDriftFromHold(taskRunID, holds, carriedOutCall)
		observationID := agentTurnRunner.nextUnusedObservationID(taskRunID, state.Observations)
		agentTurnRunner.appendEvent(taskRunID, agentcontract.ToolTaskEventName(toolName, agentcontract.ToolTaskEventRequestedSuffix), marshalEventBody(map[string]any{
			"observationID": observationID,
			"toolName":      toolName,
			"input":         json.RawMessage(carriedOutCall.ToolInput),
		}))
		observation := agentTurnRunner.saveToolObservation(
			ctx, taskRunID, observationID, "", "", "", toolName, "", carriedOutCall.ToolInput, toolName,
			canonicalToolInput(carriedOutCall.ToolInput), carriedOutCall.Result,
			false, request.WorkspaceRootPath, time.Time{}, 0,
		)
		if didDriftFromItsHold {
			observation = observationNotingApprovalDrift(observation)
		}
		agentTurnRunner.recordToolObservation(taskRunID, state, turnActionDocument{
			Action:    "continue",
			ToolName:  toolName,
			ToolInput: carriedOutCall.ToolInput,
		}, successfulToolCalls, observation, "")
	}
}
