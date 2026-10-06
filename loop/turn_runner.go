package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/iterationcost"
	"github.com/yeomyeonggeori/bluecollar/toolexposure"
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

func (agentTurnRunner *AgentTurnRunner) failLaunchStep(ctx context.Context, taskRun agentcontract.TaskRun, request AgentTurnRequest, stepName string, errorValue error) AgentTurnResult {
	reason := errorString(errorValue)
	agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLaunchStepError, marshalEventBody(map[string]string{
		"phase":    "launch",
		"stepName": strings.TrimSpace(stepName),
		"error":    reason,
	}))
	return agentTurnRunner.failTurnWithGeneratedNotice(ctx, taskRun, request, "launch", stepName, reason)
}

func (agentTurnRunner *AgentTurnRunner) failTurnWithGeneratedNotice(ctx context.Context, taskRun agentcontract.TaskRun, request AgentTurnRequest, phase string, stepName string, reason string) AgentTurnResult {
	failedTaskRun := agentTurnRunner.failTaskRunWithReason(taskRun, reason)
	noticeContext, cancelNotice := closingNoticeContextWithParent(ctx, request)
	defer cancelNotice()
	failureNotice, noticeStatus := (FailureNoticeGenerator{LanguageModel: agentTurnRunner.recoveryLanguageModel}).Generate(noticeContext, FailureReport{
		Phase:              phase,
		StepName:           stepName,
		StopReason:         reason,
		SafeFailureSummary: reason,
		RawError:           reason,
		OriginalRequest:    request.Prompt,
		ResponseLanguage:   request.ResponseLanguage,
		DiagnosticEventID:  diagnosticEventID(request, taskRun.TaskRunID, phase),
	})
	agentTurnRunner.appendEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(noticeStatus))
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, failureNotice.SendableMessage())
	result := AgentTurnResult{TaskRun: failedTaskRun, UserNotice: failedTaskRun.Result, FailureNotice: failureNotice, ToolNames: toolNamesForEvent(request.ToolSet)}
	if strings.TrimSpace(result.UserNotice) == "" {
		result.ReplySuppressed = true
		result.ReplySuppressionReason = phase + " failure notice could not be written"
	}
	return result
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

func (agentTurnRunner *AgentTurnRunner) pausedTaskResult(taskRunID string, observation turnObservation, attachments []toolcontract.FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || !isWaitingForUser(taskRun.Status) {
		return AgentTurnResult{}, false
	}
	if taskRun.Status == agentcontract.TaskStatusWaitingApproval {
		reply := firstNonEmptyString(approvalObservationUserFacingMessage(observation), taskRun.FailureReason)
		if reply == "" {
			agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentApprovalUserFacingMessageMissing, marshalEventBody(observation))
		}
		return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
	}
	reply := firstNonEmptyString(taskRun.FailureReason, toolObservationMessage(observation), observation.ContentText())
	return AgentTurnResult{TaskRun: taskRun, UserNotice: reply, Attachments: attachments, RecoveryActions: observation.RecoveryActions}, true
}

func (agentTurnRunner *AgentTurnRunner) cancelledTaskResult(taskRunID string, attachments []toolcontract.FileAttachment) (AgentTurnResult, bool) {
	taskRun, isFound := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != agentcontract.TaskStatusCancelled {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventTaskStopOutboxSuppressed, "task run was cancelled before reply delivery")
	return AgentTurnResult{TaskRun: taskRun, ReplySuppressed: true, Attachments: attachments}, true
}

// A turn that loses its context ends the task run, because nothing else will move it. The one
// exception is a deliberate cancellation of a turn someone else can name: every canceller — stop,
// supersede, revision, shutdown, admin — records the outcome itself, so claiming it here would
// race the hand that took the turn away. A deadline has no such owner, and neither does a
// delegated child, whose task run the parent's canceller never saw.
func (agentTurnRunner *AgentTurnRunner) abandonedTurnResult(ctx context.Context, taskRunID string, request AgentTurnRequest, cause error, reason string, attachments []toolcontract.FileAttachment) AgentTurnResult {
	if result, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, attachments); isCancelled {
		return result
	}
	taskRun, _ := agentTurnRunner.taskRunService.FindTaskRun(taskRunID)
	if isTaskRunFinished(taskRun.Status) {
		return AgentTurnResult{TaskRun: taskRun, UserNotice: taskRun.Result, Attachments: attachments}
	}
	isDelegatedTurn := toolcontract.IsDelegatedTurn(ctx)
	isOwnedByCanceller := errors.Is(cause, context.Canceled) && !isDelegatedTurn
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentTurnAbandoned, marshalEventBody(map[string]string{
		"reason":              reason,
		"statusWhenAbandoned": string(taskRun.Status),
		"endsTheTaskRun":      strconv.FormatBool(!isOwnedByCanceller),
	}))
	if isOwnedByCanceller {
		return AgentTurnResult{TaskRun: taskRun, ReplySuppressed: true, ReplySuppressionReason: reason, Attachments: attachments}
	}
	if isDelegatedTurn {
		return AgentTurnResult{
			TaskRun:                agentTurnRunner.failTaskRunWithReason(taskRun, reason),
			ReplySuppressed:        true,
			ReplySuppressionReason: reason,
			Attachments:            attachments,
		}
	}
	result := agentTurnRunner.failTurnWithGeneratedNotice(ctx, taskRun, request, "turn", "run_turn", reason)
	result.Attachments = attachments
	return result
}

// A delegated child has no requester of its own. Its parent reads the failure reason on the run,
// which delegatedFailureText prefers over any notice, so generating one buys nothing and — with
// the parent blocked inside runDelegatedTurn and the queue worker holding the conversation — costs
// the whole closing ceiling.
func (agentTurnRunner *AgentTurnRunner) failTaskRunWithReason(taskRun agentcontract.TaskRun, reason string) agentcontract.TaskRun {
	failedTaskRun, failError := agentTurnRunner.taskRunService.FailTaskRun(taskRun.TaskRunID, reason)
	if failError == nil {
		return failedTaskRun
	}
	taskRun.Status = agentcontract.TaskStatusFailed
	taskRun.FailureReason = firstNonEmptyString(reason, failError.Error())
	return taskRun
}

// A finished turn owns its reply. When the completing transition will not stick, the run is closed
// as blocked and the reply is carried as the notice, because work the agent did and a reply the
// judge accepted are not the repository's to discard. A run cancelled between the gate and the
// commit is the exception: the requester asked for it to stop.
func (agentTurnRunner *AgentTurnRunner) finishedTurnResult(taskRunID string, reply string, attachments []toolcontract.FileAttachment) AgentTurnResult {
	completedTaskRun, completionError := agentTurnRunner.taskRunService.CompleteTaskRun(taskRunID, reply)
	if completionError == nil {
		return AgentTurnResult{TaskRun: completedTaskRun, FinishMessage: reply, Attachments: attachments}
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCompletionPersistFailed, marshalEventBody(map[string]string{"error": completionError.Error()}))
	if cancelledResult, isCancelled := agentTurnRunner.cancelledTaskResult(taskRunID, attachments); isCancelled {
		return cancelledResult
	}
	blockedTaskRun, pauseError := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusBlocked, completionError.Error())
	if pauseError != nil {
		return AgentTurnResult{
			TaskRun:     agentcontract.TaskRun{TaskRunID: taskRunID, Status: agentcontract.TaskStatusBlocked, FailureReason: completionError.Error(), Result: reply},
			UserNotice:  reply,
			Attachments: attachments,
		}
	}
	return AgentTurnResult{TaskRun: persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply), UserNotice: reply, Attachments: attachments}
}

func isTaskRunFinished(status agentcontract.TaskStatus) bool {
	switch status {
	case agentcontract.TaskStatusCompleted, agentcontract.TaskStatusFailed, agentcontract.TaskStatusCancelled, agentcontract.TaskStatusInterrupted, agentcontract.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func (agentTurnRunner *AgentTurnRunner) sendCheckpointMessage(ctx context.Context, taskRunID string, request AgentTurnRequest, actionDocument turnActionDocument, observations []turnObservation) []turnObservation {
	message := strings.TrimSpace(actionDocument.Message)
	if message == "" || agentTurnRunner == nil {
		return observations
	}
	if taskLevelWantsSingleFinalReply(request.TaskLevel) {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSkipped, marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"reason":   "task_level_xlow",
		}))
		return observations
	}
	if !checkpointMessageAllowed(message, observations) {
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSkipped, marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"reason":   "rate_limited_or_duplicate",
		}))
		return observations
	}
	observation := newContentObservation(nextObservationIDForObservations(observations), "checkpoint", "", marshalEventBody(map[string]any{
		"message":  message,
		"toolName": actionDocument.ToolName,
	}))
	observation.Summary = message
	if request.CheckpointSender != nil {
		errorValue := request.CheckpointSender(ctx, AgentCheckpoint{
			TaskRunID: taskRunID,
			Message:   message,
			ToolName:  strings.TrimSpace(actionDocument.ToolName),
		})
		if errorValue != nil {
			observation.Output.Content = marshalEventBody(map[string]any{
				"message":  message,
				"toolName": actionDocument.ToolName,
				"status":   "failed",
				"error":    errorValue.Error(),
			})
			agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointFailed, marshalEventBody(map[string]any{
				"toolName": actionDocument.ToolName,
				"error":    errorValue.Error(),
			}))
			return append(observations, observation)
		}
		observation.Output.Content = marshalEventBody(map[string]any{
			"message":  message,
			"toolName": actionDocument.ToolName,
			"status":   "sent",
		})
		agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSent, marshalEventBody(map[string]any{
			"toolName": actionDocument.ToolName,
			"message":  message,
		}))
		return append(observations, observation)
	}
	observation.Output.Content = marshalEventBody(map[string]any{
		"message":  message,
		"toolName": actionDocument.ToolName,
		"status":   "skipped",
		"reason":   "missing_sender",
	})
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentCheckpointSkipped, marshalEventBody(map[string]any{
		"toolName": actionDocument.ToolName,
		"reason":   "missing_sender",
	}))
	return append(observations, observation)
}

func checkpointMessageAllowed(message string, observations []turnObservation) bool {
	normalizedMessage := normalizeCheckpointMessage(message)
	count := 0
	for _, observation := range observations {
		sentMessage, wasSent := midTaskMessageSent(observation)
		if !wasSent {
			continue
		}
		count++
		if normalizeCheckpointMessage(sentMessage) == normalizedMessage {
			return false
		}
	}
	return count < 3
}

func midTaskMessageSent(observation turnObservation) (string, bool) {
	if observation.Action == "checkpoint" {
		return checkpointObservationMessage(observation), true
	}
	return deliveredReplyMessage(observation)
}

func normalizeCheckpointMessage(message string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(message))), " ")
}

func checkpointObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(observation.StructuredOutput(), &document) == nil {
		return document.Message
	}
	return observation.Summary
}

func isWaitingForUser(status agentcontract.TaskStatus) bool {
	return status == agentcontract.TaskStatusWaitingApproval || status == agentcontract.TaskStatusWaitingUserInput
}

func toolObservationMessage(observation turnObservation) string {
	var document struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(observation.StructuredOutput(), &document) != nil {
		return ""
	}
	return strings.TrimSpace(document.Message)
}

func finishActionMessage(actionDocument turnActionDocument) string {
	return strings.TrimSpace(actionDocument.Message)
}

func approvalObservationUserFacingMessage(observation turnObservation) string {
	var document struct {
		UserFacingMessage string `json:"userFacingMessage"`
		Message           string `json:"message"`
		Question          string `json:"question"`
	}
	if json.Unmarshal(observation.StructuredOutput(), &document) != nil {
		return ""
	}
	return firstNonEmptyString(document.UserFacingMessage, document.Message, document.Question)
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

func (agentTurnRunner *AgentTurnRunner) requestForStep(_ context.Context, request AgentTurnRequest, state *agentTaskState) AgentTurnRequest {
	plannedRequest := requestWithStepWorkingSetTools(request, *state)
	elapsed := agentTurnRunner.turnElapsed(request.EffortStartedAt)
	pressureStage := limitPressureStageFor(state.IterationCount, state.ToolCallCount, elapsed, agentTurnRunner.reachableLimits(*state))
	stepKey := stepToolExposureKey(plannedRequest, *state)
	if state.StepExposure.Key != stepKey {
		state.StepExposure = stepToolExposureFor(plannedRequest, *state, stepKey)
	}
	iterationRequest := plannedRequest
	iterationRequest.ToolSet = state.StepExposure.ToolSet
	iterationRequest.ToolExposure = state.StepExposure.Exposure
	if len(state.PendingBatchedActions) > 0 {
		iterationRequest.ToolSet, iterationRequest.ToolExposure = pendingBatchedToolExposure(plannedRequest.ToolSet, *state)
	}
	if pressureStage == limitPressureStageNarrowPalette && len(state.PendingBatchedActions) == 0 {
		iterationRequest.ToolSet = iterationRequest.ToolSet.WithAllowedToolNames(wrapUpDeliveryToolNames(plannedRequest))
	}
	iterationRequest.StepBudgetContext = agentTurnRunner.stepBudgetContext(*state)
	iterationRequest.RestrictActionToTerminalOnly = state.ShouldRestrictNextActionToTerminal
	return iterationRequest
}

func pendingBatchedToolExposure(toolSet *toolcontract.ToolSet, state agentTaskState) (*toolcontract.ToolSet, ToolExposureEvent) {
	exposedToolNames := []string{}
	for _, toolName := range state.PendingBatchedToolNames {
		if toolSet.CanExpose(toolName) {
			exposedToolNames = appendUniqueStrings(exposedToolNames, toolName)
		}
	}
	exposure := state.PendingBatchedToolExposure
	exposure.ExposedToolIDs = append([]string{}, exposedToolNames...)
	if len(exposedToolNames) == 0 {
		return toolSet.WithRegisteredToolNamesLimitedTo(nil), exposure
	}
	return toolSet.WithAllowedToolNames(exposedToolNames), exposure
}

func stepToolExposureKey(plannedRequest AgentTurnRequest, state agentTaskState) string {
	instructionBundle := instructionBundleFromTurnRequest(plannedRequest)
	return strings.Join([]string{
		state.ActivePlanStepTitle,
		strings.Join(sortedStrings(plannedRequest.PinnedToolNames), ","),
		strings.Join(sortedStrings(activeRecoveryToolNames(state.Observations)), ","),
		firstPendingRequiredToolName(instructionBundle.RequiredNextTools, state.Observations),
	}, "\x00")
}

func stepToolExposureFor(plannedRequest AgentTurnRequest, state agentTaskState, stepKey string) stepToolExposure {
	filteredToolSet, exposureEvent := toolSetForAgentTurnWithExposure(
		plannedRequest.ToolSet,
		instructionBundleFromTurnRequest(plannedRequest),
		agentRequestFromTurnRequest(plannedRequest),
		ExecutionPlan{},
		false,
		plannedRequest.OutcomeContract,
		ToolExposureEvent{},
		state.Observations,
	)
	return stepToolExposure{Key: stepKey, ToolSet: filteredToolSet, Exposure: exposureEvent}
}

func sortedStrings(values []string) []string {
	sorted := append([]string{}, values...)
	sort.Strings(sorted)
	return sorted
}

func wrapUpDeliveryToolNames(request AgentTurnRequest) []string {
	toolNames := []string{}
	if expectedResultRequiresFileAttachment(request.OutcomeContract) {
		toolNames = appendUniqueStrings(toolNames, availableFileDeliveryToolNames(request)...)
	}
	if externalSendCompletionEvidenceRequired(request) {
		toolNames = appendUniqueStrings(toolNames, requiredSendToolNamesForRequest(request)...)
	}
	return toolNames
}

func (agentTurnRunner *AgentTurnRunner) stepBudgetContext(state agentTaskState) string {
	limits := agentTurnRunner.reachableLimits(state)
	maxToolCallCount := limits.MaxToolCallCount
	remainingToolCallCount := maxToolCallCount - state.ToolCallCount
	if remainingToolCallCount < 0 {
		remainingToolCallCount = 0
	}
	maxIterationCount := limits.MaxIterationCount
	remainingIterationCount := maxIterationCount - state.IterationCount
	if remainingIterationCount < 0 {
		remainingIterationCount = 0
	}
	return strings.Join([]string{
		"Step budget:",
		fmt.Sprintf("Tool calls: %d/%d used, %d remaining.", state.ToolCallCount, maxToolCallCount, remainingToolCallCount),
		fmt.Sprintf("Steps: %d/%d used, %d remaining.", state.IterationCount, maxIterationCount, remainingIterationCount),
		"Use the shortest path to the expected result. Avoid extra inspection when the next edit, build, publish, file delivery, or final action is already clear.",
		"Keep at least two tool calls for delivery when the requested link or file has not been delivered yet.",
	}, "\n")
}

func requestWithStepWorkingSetTools(request AgentTurnRequest, state agentTaskState) AgentTurnRequest {
	observations := state.Observations
	request.PinnedToolNames = planStepPinnedToolNames(request, state)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, pendingFileDeliveryToolNames(request, observations)...)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, observedSuggestedNextToolNames(observations)...)
	foundToolNames := foundToolNamesFromObservations(observations)
	request.PinnedToolNames = appendUniqueStrings(request.PinnedToolNames, foundToolNames...)
	request.SkillDecisions = withOwningSkillDecisions(request.SkillDecisions, request.AvailableSkills, foundToolNames)
	return request
}

func planStepPinnedToolNames(request AgentTurnRequest, state agentTaskState) []string {
	if len(state.PlanStepToolNames) == 0 {
		return appendUniqueStrings(request.PinnedToolNames)
	}
	return appendUniqueStrings(toolNamesExcept(request.PinnedToolNames, request.LikelyToolNames), state.PlanStepToolNames...)
}

func toolNamesExcept(toolNames []string, excludedToolNames []string) []string {
	remaining := []string{}
	for _, toolName := range toolNames {
		if stringSliceContains(excludedToolNames, toolName) {
			continue
		}
		remaining = append(remaining, toolName)
	}
	return remaining
}

func withOwningSkillDecisions(decisions []SkillSelectionDecision, availableSkills []SkillInstruction, requestedToolNames []string) []SkillSelectionDecision {
	if len(requestedToolNames) == 0 {
		return decisions
	}
	selectedSkillNames := map[string]bool{}
	for _, decision := range decisions {
		if decision.Status == "selected" {
			selectedSkillNames[decision.Name] = true
		}
	}
	amendedDecisions := append([]SkillSelectionDecision{}, decisions...)
	for _, skillInstruction := range availableSkills {
		if selectedSkillNames[skillInstruction.Name] {
			continue
		}
		for _, toolName := range SkillToolNames(skillInstruction) {
			if !stringSliceContains(requestedToolNames, toolName) {
				continue
			}
			amendedDecisions = append(amendedDecisions, SkillSelectionDecision{
				Name:   skillInstruction.Name,
				Status: "selected",
				Reason: "owns requested tool " + toolName,
			})
			selectedSkillNames[skillInstruction.Name] = true
			break
		}
	}
	return amendedDecisions
}

func foundToolNamesFromObservations(observations []turnObservation) []string {
	toolNames := []string{}
	for _, observation := range observations {
		if observation.Action != "continue" || observation.Failed() || !toolexposure.ToolNamesMatch(observation.Tool, toolcontract.EquipToolName) {
			continue
		}
		var foundTools agentcontract.EquippedTools
		if json.Unmarshal(observation.Output.Data, &foundTools) != nil {
			continue
		}
		for _, selectedTool := range foundTools.SelectedTools {
			toolNames = appendUniqueStrings(toolNames, selectedTool.Name)
		}
	}
	return toolNames
}

func pendingFileDeliveryToolNames(request AgentTurnRequest, observations []turnObservation) []string {
	if !expectedResultRequiresFileAttachment(request.OutcomeContract) || hasSuccessfulArtifactDeliveryObservation(observations) {
		return nil
	}
	return availableFileDeliveryToolNames(request)
}

func availableFileDeliveryToolNames(request AgentTurnRequest) []string {
	toolNames := []string{toolcontract.BashToolName, toolcontract.FileDeliverToolName}
	if request.ToolSet == nil {
		return toolNames
	}
	return registeredToolNamesOnly(request.ToolSet, toolNames)
}

func hasSuccessfulArtifactDeliveryObservation(observations []turnObservation) bool {
	for _, observation := range observations {
		if !observation.Failed() && toolexposure.IsArtifactDeliveryTool(observation.Tool) {
			return true
		}
	}
	return false
}

func instructionBundleFromTurnRequest(request AgentTurnRequest) InstructionBundle {
	contractToolWorkingSet := request.ContractToolWorkingSet
	return InstructionBundle{
		Prompt:                      request.InstructionPrompt,
		Skills:                      append([]SkillInstruction{}, request.AvailableSkills...),
		Sources:                     append([]InstructionSource{}, request.InstructionSources...),
		SkillDecisions:              append([]SkillSelectionDecision{}, request.SkillDecisions...),
		RequiredNextTools:           append([]string{}, contractToolWorkingSet.RequiredNextTools...),
		RequiredEvidenceTools:       append([]string{}, contractToolWorkingSet.RequiredEvidenceTools...),
		HasContractSkillArbitration: contractToolWorkingSet.IsAuthoritative(),
		RetrievalMode:               request.SkillRetrievalMode,
		IndexStatus:                 request.SkillIndexStatus,
		CandidateCount:              request.SkillCandidateCount,
		SkillQueries:                append([]string{}, request.SkillQueries...),
	}
}

func agentRequestFromTurnRequest(request AgentTurnRequest) AgentRequest {
	return AgentRequest{
		RequesterPersonID:    request.RequesterPersonID,
		RequesterName:        request.RequesterName,
		RequesterCallingName: request.RequesterCallingName,
		RequesterHandle:      request.RequesterHandle,
		RequesterCircles:     append([]string{}, request.RequesterCircles...),
		ExistingTaskRunID:    request.ExistingTaskRunID,
		ProfileName:          request.ProfileName,
		ConversationID:       request.ConversationID,
		ConversationType:     request.ConversationType,
		Prompt:               request.Prompt,
		ResponseLanguage:     request.ResponseLanguage,
		VisibleContext:       request.VisibleContext,
		MemoryFacts:          append([]MemoryFact{}, request.MemoryFacts...),
		ToolSet:              request.ToolSet,
		PinnedToolNames:      append([]string{}, request.PinnedToolNames...),
		LikelyToolNames:      append([]string{}, request.LikelyToolNames...),
		PinnedSkillNames:     append([]string{}, request.PinnedSkillNames...),
		WorkspaceRootPath:    request.WorkspaceRootPath,
		ActivePaths:          append([]string{}, request.ActivePaths...),
		InstructionPrompt:    request.InstructionPrompt,
		ActiveGoal:           request.ActiveGoal,
		TaskShape:            request.TaskShape,
		TurnStartedAt:        request.TurnStartedAt,
		CheckpointSender:     request.CheckpointSender,
		TaskRunChosen:        request.TaskRunChosen,
	}
}

func (agentTurnRunner *AgentTurnRunner) buildTurnMessages(request AgentTurnRequest, observations []turnObservation, executionState ExecutionState) []model.Message {
	return (PromptAssembler{}).BuildTurnMessages(
		request,
		observations,
		systemInstructionFor(agentTurnRunner.options, request).Text(),
		buildAgentToolDescription(request.ToolSet),
		executionState,
	)
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

const maxStallRecoveryDirectivesPerEpisode = 4

func (agentTurnRunner *AgentTurnRunner) continueStalledRecoveryIfAllowed(taskRunID string, state *agentTaskState, tracker *actionProgressTracker, allowance recoveryAllowance) bool {
	if !allowance.CanRecover {
		return false
	}
	failureDebt, hasFailureDebt := activeFailureDebt(state.Observations)
	if !hasFailureDebt {
		return false
	}
	if !stalledOnRedundantInspection(state.Observations) {
		return false
	}
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	directive := stalledRecoveryDirectiveObservation(nextObservationIDForObservations(state.Observations), failureDebt)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallRecoveryDirective, marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func (agentTurnRunner *AgentTurnRunner) steerStalledTurnTowardNextTool(taskRunID string, state *agentTaskState, tracker *actionProgressTracker) bool {
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	suggestion, isFound := latestObservedSuggestedNextTool(state.Observations)
	if !isFound {
		return false
	}
	if state.Request.ToolSet != nil && !state.Request.ToolSet.IsAllowed(suggestion.ToolName) {
		return false
	}
	directive := suggestedNextToolDirectiveObservation(nextObservationIDForObservations(state.Observations), suggestion)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentSuggestedNextToolDirective, marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func (agentTurnRunner *AgentTurnRunner) steerStalledTurnTowardExit(taskRunID string, state *agentTaskState, tracker *actionProgressTracker) bool {
	if tracker.stallRecoveryDirectiveCount >= maxStallRecoveryDirectivesPerEpisode {
		return false
	}
	directive := stalledExitDirectiveObservation(nextObservationIDForObservations(state.Observations), state.Observations)
	state.Observations = append(state.Observations, directive)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallExitDirective, marshalEventBody(directive))
	tracker.noteStallRecoveryDirective(state.Observations)
	return true
}

func suggestedNextToolDirectiveObservation(observationID string, suggestion observedSuggestedNextTool) turnObservation {
	message := suggestion.Reason + " Call " + suggestion.ToolName + " now before repeating inspection, asking the user, or closing the task."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"suggestedTool":       suggestion.ToolName,
		"sourceTool":          suggestion.SourceTool,
		"sourceObservationID": suggestion.ObservationID,
	}))
	observation.Summary = message
	return observation
}

func stalledExitDirectiveObservation(observationID string, observations []turnObservation) turnObservation {
	failedTool := ""
	if failureDebt, hasFailureDebt := activeFailureDebt(observations); hasFailureDebt {
		failedTool = strings.TrimSpace(failureDebt.LatestFailure.Tool)
	}
	message := "You are repeating actions without making progress. Stop retrying the same thing and stop re-emitting a final reply that keeps getting rejected. Take one of two exits now: either take a genuinely different action that changes workspace, tool, or evidence state; or, if you cannot obtain what you need because a tool keeps failing or the required evidence is unavailable, end immediately with fail and failureResolution=failure_report, giving the user a short honest explanation of what you could not do. Do not loop and do not ask the user how to proceed."
	missingOperationName := latestMissingRequiredEvidenceOperationName(observations)
	if missingOperationName != "" {
		message = "You have not yet called " + missingOperationName + ". Call that direct tool with the appropriate input before attempting to close again. If it is genuinely not needed for this request, end with fail and failureResolution=failure_report, explaining why in the user reply. Do not re-emit a final reply again without this evidence."
	}
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":                message,
		"failedTool":               failedTool,
		"missingEvidenceOperation": missingOperationName,
	}))
	observation.Summary = message
	return observation
}

func latestMissingRequiredEvidenceOperationName(observations []turnObservation) string {
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if observation.Action != "evidence_missing" || observation.RecoveryPacket == nil {
			continue
		}
		if len(observation.RecoveryPacket.AllowedTools) > 0 {
			return strings.TrimSpace(observation.RecoveryPacket.AllowedTools[0])
		}
	}
	return ""
}

func stalledOnRedundantInspection(observations []turnObservation) bool {
	if len(observations) == 0 {
		return false
	}
	lastObservation := observations[len(observations)-1]
	if lastObservation.Action != "policy" || lastObservation.Tool != "file_read" {
		return false
	}
	document := map[string]any{}
	if json.Unmarshal(lastObservation.Output.Data, &document) != nil {
		return false
	}
	return stringValue(document["cacheStatus"]) == "hit"
}

func stalledRecoveryDirectiveObservation(observationID string, failureDebt FailureDebt) turnObservation {
	failedTool := strings.TrimSpace(failureDebt.LatestFailure.Tool)
	message := "You are repeating actions without progress while " + failedTool + " is still failing. You already have the information you need. Make one concrete fix now by editing the offending file with edit, then re-run " + failedTool + ". Do not read the same content again and do not ask the user how to proceed."
	observation := newContentObservation(observationID, "policy", "", marshalEventBody(map[string]string{
		"directive":           message,
		"failedTool":          failedTool,
		"failedObservationID": failureDebt.LatestFailure.ObservationID,
	}))
	observation.Summary = message
	return observation
}

func (agentTurnRunner *AgentTurnRunner) shouldPauseForStalledRecovery(taskRunID string, observations []turnObservation) bool {
	failureDebt, hasFailureDebt := activeFailureDebt(observations)
	if !hasFailureDebt {
		return false
	}
	if failureClassForObservation(failureDebt.LatestFailure) != failureClassUserInput {
		return false
	}
	for _, taskEvent := range agentTurnRunner.taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventAgentNoProgressLoopPaused {
			return false
		}
	}
	return true
}

func (agentTurnRunner *AgentTurnRunner) pauseTurnForStall(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, reason string, progressEvaluation actionProgressEvaluation, allowance recoveryAllowance, state agentTaskState) (AgentTurnResult, bool) {
	notice, replyStatus, hasReply := agentTurnRunner.generateStallPauseNotice(ctx, taskRunID, request, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallPauseReply, marshalEventBody(replyStatus))
	if !hasReply {
		return AgentTurnResult{}, false
	}
	pausedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingUserInput, reason)
	if errorValue != nil {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentNoProgressLoopPaused, marshalEventBody(map[string]any{
		"reason":             reason,
		"progressEvaluation": progressEvaluation,
		"recoveryAllowance":  allowance,
	}))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentGoalWaitingUserInput, marshalEventBody(stalledWaitingGoal(taskRunID, request)))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusWaitingUserInput, "no_progress_loop_paused", reason)
	reply := notice.SendableMessage()
	pausedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, pausedTaskRun, reply)
	return AgentTurnResult{TaskRun: pausedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
}

func (agentTurnRunner *AgentTurnRunner) blockTurnForStall(ctx context.Context, taskRunID string, stepID string, request AgentTurnRequest, reason string, progressEvaluation actionProgressEvaluation, allowance recoveryAllowance, state agentTaskState) (AgentTurnResult, bool) {
	notice, replyStatus, hasReply := agentTurnRunner.generateStallPauseNotice(ctx, taskRunID, request, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentStallBlockedReply, marshalEventBody(replyStatus))
	blockedTaskRun, errorValue := agentTurnRunner.taskRunService.PauseTaskRun(taskRunID, agentcontract.TaskStatusBlocked, reason)
	if errorValue != nil {
		return AgentTurnResult{}, false
	}
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentNoProgressLoopStopped, marshalEventBody(map[string]any{
		"reason":             reason,
		"progressEvaluation": progressEvaluation,
		"recoveryAllowance":  allowance,
	}))
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentGoalBlocked, marshalEventBody(blockedGoal(taskRunID, request, reason)))
	agentTurnRunner.saveStep(taskRunID, stepID, agentcontract.TaskStatusBlocked, "no_progress_loop_stopped", reason)
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "stall", reason, replyStatus)
		failureReport := buildFailureReport(request, taskRunID, "stall", reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState, recoveryDecision{})
		notice = buildRawErrorFailureNotice(failureReport)
		fallbackReply := notice.SendableMessage()
		blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, fallbackReply)
		return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: fallbackReply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
	}
	reply := notice.SendableMessage()
	blockedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, blockedTaskRun, reply)
	return AgentTurnResult{TaskRun: blockedTaskRun, UserNotice: reply, FailureNotice: notice, RecoveryActions: recoveryActionsFromObservations(state.Observations)}, true
}

func stalledWaitingGoal(taskRunID string, request AgentTurnRequest) ActiveGoal {
	waitingGoal := request.ActiveGoal
	waitingGoal.GoalID = firstNonEmptyString(waitingGoal.GoalID, taskRunID)
	waitingGoal.TaskRunID = firstNonEmptyString(waitingGoal.TaskRunID, taskRunID)
	waitingGoal.OriginalInstruction = firstNonEmptyString(waitingGoal.OriginalInstruction, request.Prompt)
	waitingGoal.Status = ActiveGoalStatusWaitingUserInput
	return waitingGoal
}

func blockedGoal(taskRunID string, request AgentTurnRequest, reason string) ActiveGoal {
	blockedGoal := request.ActiveGoal
	blockedGoal.GoalID = firstNonEmptyString(blockedGoal.GoalID, taskRunID)
	blockedGoal.TaskRunID = firstNonEmptyString(blockedGoal.TaskRunID, taskRunID)
	blockedGoal.OriginalInstruction = firstNonEmptyString(blockedGoal.OriginalInstruction, request.Prompt)
	blockedGoal.CurrentObjective = firstNonEmptyString(blockedGoal.CurrentObjective, reason)
	blockedGoal.Status = ActiveGoalStatusBlocked
	return blockedGoal
}

func (agentTurnRunner *AgentTurnRunner) finalizeIfSatisfiedOrFail(ctx context.Context, request AgentTurnRequest, reason string, state *agentTaskState, usedIterationCount int) (AgentTurnResult, error) {
	effortContext, cancelEffort := agentTurnRunner.currentEffortContext(ctx, request.EffortStartedAt)
	result, isCompleted := agentTurnRunner.completeAtStop(effortContext, state.TaskRunID, request, state)
	effortError := effortContext.Err()
	cancelEffort()
	if isCompleted {
		return result, nil
	}
	if ctx.Err() != nil {
		return agentTurnRunner.abandonedTurnResult(ctx, state.TaskRunID, request, ctx.Err(), "the turn's caller context ended before the agent could finish: "+errorString(ctx.Err()), state.Attachments), nil
	}
	if errors.Is(effortError, context.DeadlineExceeded) || agentTurnRunner.currentEffortElapsed(request.EffortStartedAt) {
		return agentTurnRunner.stopAtElapsedLimit(ctx, state.TaskRunID, request, state, usedIterationCount)
	}
	return agentTurnRunner.failTurnWithContext(ctx, state.TaskRunID, request, reason, state.Observations, attachmentsAlreadyDelivered(state.Attachments, state.DeliveredAttachmentPaths), state.ExecutionState)
}

func (agentTurnRunner *AgentTurnRunner) failTurnWithContext(ctx context.Context, taskRunID string, request AgentTurnRequest, reason string, observations []turnObservation, attachments []toolcontract.FileAttachment, executionState ExecutionState) (AgentTurnResult, error) {
	failureNotice, replyStatus, hasReply := agentTurnRunner.generateFailureNotice(ctx, taskRunID, request, reason, observations, attachments, executionState)
	agentTurnRunner.appendEvent(taskRunID, agentcontract.TaskEventAgentFailureReply, marshalEventBody(replyStatus))
	reply := failureNotice.SendableMessage()
	if !hasReply {
		agentTurnRunner.appendUnavailableReplyEvents(taskRunID, "failure", reason, replyStatus)
		failureReport := buildFailureReport(request, taskRunID, "failure", reason, observations, attachments, executionState, recoveryDecision{})
		failureNotice = buildRawErrorFailureNotice(failureReport)
		reply = failureNotice.SendableMessage()
	}
	failedTaskRun, _ := agentTurnRunner.taskRunService.FailTaskRun(taskRunID, reason)
	failedTaskRun = persistTaskRunResult(agentTurnRunner.taskRunService, failedTaskRun, reply)
	result := AgentTurnResult{TaskRun: failedTaskRun, UserNotice: reply, FailureNotice: failureNotice, RecoveryActions: recoveryActionsFromObservations(observations)}
	return result, nil
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

func persistTaskRunResult(taskRunService taskstate.TaskRunStore, taskRun agentcontract.TaskRun, result string) agentcontract.TaskRun {
	persistedTaskRun, errorValue := taskRunService.RecordTaskRunResult(taskRun.TaskRunID, result)
	if errorValue != nil {
		taskRun.Result = result
		return taskRun
	}
	return persistedTaskRun
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
