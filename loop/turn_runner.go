package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
		if warning := agentTurnRunner.nextLimitPressureWarning(state, iteration-1, state.ToolCallCount, agentTurnRunner.turnElapsed(request.EffortStartedAt), len(state.Observations)+1, limitPressureWarnings); warning != nil {
			if warning.Observation != nil {
				state.Observations = append(state.Observations, *warning.Observation)
			}
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
				if !agentTurnRunner.currentEffortElapsed(request.EffortStartedAt) {
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
			deliveredDocument, delivery, isDelivered := agentTurnRunner.deliverReplyAttachments(workContext, taskRun.TaskRunID, iterationRequest, &state, successfulToolCalls, actionDocument)
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
			reply, carried := agentTurnRunner.replyForFinish(workContext, taskRun.TaskRunID, request, &state, completionGateResult, reply, delivery.ReplyNotes)
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
