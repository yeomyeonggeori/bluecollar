package loop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type turnRun struct {
	runner                     *AgentTurnRunner
	callerContext              context.Context
	turnContext                context.Context
	taskContext                context.Context
	workContext                context.Context
	cancelTask                 context.CancelFunc
	cancelWork                 context.CancelFunc
	unregisterTaskCancel       func()
	request                    AgentTurnRequest
	taskRun                    agentcontract.TaskRun
	isPausedTaskResume         bool
	state                      agentTaskState
	successfulToolCalls        map[string]turnObservation
	limitPressureWarnings      map[string]bool
	hasRetiredWarningsByGrant  bool
	progressTracker            actionProgressTracker
	appliedSteerEventIDs       map[string]bool
	iterationStartedAt         time.Time
	hasIterationSpentModelCall bool
}

type turnOutcome struct {
	result     AgentTurnResult
	err        error
	isFinished bool
}

var keepGoing = turnOutcome{}

func finishedWith(result AgentTurnResult, err error) turnOutcome {
	return turnOutcome{result: result, err: err, isFinished: true}
}

func (agentTurnRunner *AgentTurnRunner) RunTurn(ctx context.Context, request AgentTurnRequest) (AgentTurnResult, error) {
	if agentTurnRunner.languageModel == nil {
		return AgentTurnResult{}, errors.New("language model provider is not configured")
	}
	run := agentTurnRunner.newTurnRun(ctx, request)
	defer run.close()
	if launchFailure, hasFailedToLaunch := run.start(); hasFailedToLaunch {
		return launchFailure, nil
	}
	return run.loop()
}

func (agentTurnRunner *AgentTurnRunner) newTurnRun(ctx context.Context, request AgentTurnRequest) *turnRun {
	request = preparedTurnRequest(requestReducedToCallableTools(request))
	taskRun := agentTurnRunner.taskRunForRequest(request)
	if request.TaskRunChosen != nil {
		request.TaskRunChosen(taskRun.TaskRunID)
	}
	run := &turnRun{
		runner:             agentTurnRunner,
		callerContext:      ctx,
		request:            request,
		taskRun:            taskRun,
		isPausedTaskResume: taskRun.Status == agentcontract.TaskStatusWaitingApproval || taskRun.Status == agentcontract.TaskStatusWaitingUserInput,
	}
	run.recordLaunchEvents()
	run.openTurnContext()
	return run
}

func preparedTurnRequest(request AgentTurnRequest) AgentTurnRequest {
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
	return request
}

func (run *turnRun) recordLaunchEvents() {
	taskRunID := run.taskRun.TaskRunID
	if run.request.TurnAnchorClamped {
		run.runner.appendEvent(taskRunID, agentcontract.TaskEventAgentTurnAnchorClamped, marshalEventBody(map[string]any{
			"phase":                       "execution",
			"originalTurnStartedAtUnixMs": run.request.OriginalTurnStartedAt.UnixMilli(),
			"clampedTurnStartedAtUnixMs":  run.request.TurnStartedAt.UnixMilli(),
			"nowUnixMs":                   time.Now().UnixMilli(),
		}))
	}
	run.runner.appendTaskSourceEvent(taskRunID, run.request.SourceReference)
	run.runner.appendConversationBudgetEvent(taskRunID, run.runner.options.ContextWindowTokens)
}

func (run *turnRun) openTurnContext() {
	request := run.request
	run.turnContext = model.ContextWithRequestContext(run.callerContext, model.RequestContext{
		RequesterPersonID:       request.RequesterPersonID,
		RequesterEmail:          request.RequesterEmail,
		RequesterName:           request.RequesterName,
		RequesterPlatformUserID: request.RequesterPlatformUserID,
		ConversationID:          request.ConversationID,
		Platform:                request.Platform,
	})
	observeRecord := run.runner.llmCallObserverForTaskRun(run.taskRun.TaskRunID)
	run.runner.observeLanguageModels(observeRecord)
	run.turnContext = agentcontract.WithLLMCallObserver(run.turnContext, observeRecord)
	run.taskContext, run.cancelTask = context.WithCancel(run.turnContext)
	run.runner.beginExpectedChanges(run.taskContext, run.taskRun.TaskRunID, request)
	run.unregisterTaskCancel = run.runner.taskRunService.RegisterTaskRunCancel(run.taskRun.TaskRunID, run.cancelTask)
}

func (agentTurnRunner *AgentTurnRunner) observeLanguageModels(observeRecord llmCallObserver) {
	agentTurnRunner.languageModel = observeLanguageModel(agentTurnRunner.languageModel, observeRecord)
	if agentTurnRunner.recoveryLanguageModel == nil {
		agentTurnRunner.recoveryLanguageModel = agentTurnRunner.languageModel
		return
	}
	agentTurnRunner.recoveryLanguageModel = observeLanguageModel(agentTurnRunner.recoveryLanguageModel, observeRecord)
}

func (run *turnRun) close() {
	if run.cancelWork != nil {
		run.cancelWork()
	}
	run.unregisterTaskCancel()
	run.runner.forgetExpectedChanges(run.taskRun.TaskRunID)
	run.cancelTask()
}

func (run *turnRun) openWorkContext() {
	run.workContext, run.cancelWork = run.runner.currentEffortContext(run.taskContext, run.request.EffortStartedAt)
}

func (run *turnRun) refreshWorkContext() {
	run.cancelWork()
	run.openWorkContext()
}

func (run *turnRun) start() (AgentTurnResult, bool) {
	taskRunID := run.taskRun.TaskRunID
	runningTaskRun, errorValue := run.runner.taskRunService.AdvanceTaskRun(taskRunID, "assistant")
	if errorValue != nil {
		return run.runner.failLaunchStep(run.turnContext, run.taskRun, run.request, "start_attempt", errorValue), true
	}
	run.taskRun = runningTaskRun
	run.openWorkContext()
	run.runner.appendInstructionEvent(taskRunID, run.request)
	state, errorValue := agentTaskStateForTurn(run.request, run.runner.options, run.taskRun, run.runner.taskRunService.ListTaskEvent(taskRunID), run.isPausedTaskResume)
	if errorValue != nil {
		return run.runner.failLaunchStep(run.turnContext, run.taskRun, run.request, "restore_state", errorValue), true
	}
	run.restoreProgress(state)
	return AgentTurnResult{}, false
}

func (run *turnRun) restoreProgress(state agentTaskState) {
	taskRunID := run.taskRun.TaskRunID
	run.state = state
	run.runner.bringBackImagesTheTurnAlreadyRead(run.workContext, taskRunID, run.state.Observations)
	run.successfulToolCalls = map[string]turnObservation{}
	run.runner.recordCarriedOutCalls(run.workContext, taskRunID, run.request, &run.state, run.successfulToolCalls)
	run.limitPressureWarnings = map[string]bool{}
	run.progressTracker = newActionProgressTracker(run.state.Observations)
	run.appliedSteerEventIDs = appliedSteerEventIDsFromTaskEvents(run.runner.taskRunService.ListTaskEvent(taskRunID))
	run.iterationStartedAt = time.Now()
}

func (run *turnRun) loop() (AgentTurnResult, error) {
	for iteration := 1; ; iteration++ {
		if outcome := run.runIteration(iteration); outcome.isFinished {
			return outcome.result, outcome.err
		}
	}
}

func (run *turnRun) runIteration(iteration int) turnOutcome {
	run.settleIterationCost(iteration)
	if outcome := run.stopBeforeIteration(iteration); outcome.isFinished {
		return outcome
	}
	run.applyIterationNotices(iteration)
	stepID := fmt.Sprintf("%s:turn-%03d", run.taskRun.TaskRunID, iteration)
	run.runner.saveStep(run.taskRun.TaskRunID, stepID, agentcontract.TaskStatusRunning, "agent turn iteration", "")
	if run.workContext.Err() != nil {
		return run.abandon(run.workContext.Err(), "the turn's work context ended before the agent could act: "+errorString(run.workContext.Err()))
	}
	iterationRequest := run.runner.requestForStep(run.request, &run.state)
	run.state.ShouldRestrictNextActionToTerminal = false
	run.runner.appendEvent(run.taskRun.TaskRunID, agentcontract.TaskEventAgentStepWorkingSet, marshalEventBody(map[string]any{
		"step":     iteration,
		"exposure": iterationRequest.ToolExposure,
	}))
	actionDocument, hasAction, outcome := run.chooseAction(iteration, stepID, iterationRequest)
	if !hasAction {
		return outcome
	}
	run.recordAction(actionDocument)
	return run.applyAction(iteration, stepID, iterationRequest, actionDocument)
}

func (run *turnRun) abandon(cause error, reason string) turnOutcome {
	return finishedWith(run.runner.abandonedTurnResult(run.taskContext, run.taskRun.TaskRunID, run.request, cause, reason, run.state.Attachments), nil)
}

func (run *turnRun) settleIterationCost(iteration int) {
	if iteration <= 1 {
		return
	}
	if run.hasIterationSpentModelCall {
		run.runner.recordIterationCost(run.iterationStartedAt)
		budgetBeforeRefresh := run.runner.options.MaxElapsedSecond
		run.runner.refreshElapsedBudget(run.state.budgetTaskLevel())
		if run.runner.options.MaxElapsedSecond > budgetBeforeRefresh {
			run.refreshWorkContext()
		}
	}
	run.iterationStartedAt = time.Now()
	run.hasIterationSpentModelCall = false
}

func (run *turnRun) stopBeforeIteration(iteration int) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	if cancelledResult, isCancelled := run.runner.cancelledTaskResult(taskRunID, run.state.Attachments); isCancelled {
		return finishedWith(cancelledResult, nil)
	}
	if run.callerContext.Err() != nil {
		return run.abandon(run.callerContext.Err(), "the turn's caller context ended before the agent could act: "+errorString(run.callerContext.Err()))
	}
	if result, isElapsed, errorValue := run.runner.stopForElapsedLimitIfReached(run.taskContext, taskRunID, run.request, &run.state, iteration-1); isElapsed {
		return finishedWith(result, errorValue)
	}
	if iteration > run.runner.options.MaxIterationCount && !run.runner.extendBudgetOneLevelOnce(taskRunID, &run.state) {
		return run.stopAtIterationLimit(iteration)
	}
	return keepGoing
}

func (run *turnRun) stopAtIterationLimit(iteration int) turnOutcome {
	taskRunID := run.taskRun.TaskRunID
	result, errorValue := run.runner.completeOrStopForLimit(run.workContext, taskRunID, run.request, "max_iterations", &run.state, iteration-1)
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		if elapsedResult, isElapsed, elapsedError := run.runner.stopForElapsedLimitIfReached(run.taskContext, taskRunID, run.request, &run.state, iteration-1); isElapsed {
			return finishedWith(elapsedResult, elapsedError)
		}
	}
	return finishedWith(result, errorValue)
}

func (run *turnRun) applyIterationNotices(iteration int) {
	taskRunID := run.taskRun.TaskRunID
	run.state.Observations = run.runner.applyPendingSteeringEvents(taskRunID, run.state.Observations, run.appliedSteerEventIDs)
	run.state.IterationCount = iteration - 1
	if run.state.didExtendBudgetOneLevel() && !run.hasRetiredWarningsByGrant {
		run.retireWarningsForGrant()
	}
	run.warnAboutLimitPressure(iteration)
}

func (run *turnRun) retireWarningsForGrant() {
	run.hasRetiredWarningsByGrant = true
	run.refreshWorkContext()
	run.limitPressureWarnings = map[string]bool{}
	grantedBudget := grantedBudgetObservation(run.state.Observations, run.runner.options)
	run.state.Observations = append(run.state.Observations, grantedBudget)
	run.runner.appendEvent(run.taskRun.TaskRunID, agentcontract.TaskEventAgentBudgetUpdateSent, marshalEventBody(grantedBudget))
}

func (run *turnRun) warnAboutLimitPressure(iteration int) {
	elapsed := run.runner.turnElapsed(run.request.EffortStartedAt)
	warning := run.runner.nextLimitPressureWarning(run.state, iteration-1, run.state.ToolCallCount, elapsed, len(run.state.Observations)+1, run.limitPressureWarnings)
	if warning == nil {
		return
	}
	if warning.Observation != nil {
		run.state.Observations = append(run.state.Observations, *warning.Observation)
	}
	run.runner.appendEvent(run.taskRun.TaskRunID, agentcontract.TaskEventAgentLimitPressure, marshalEventBody(warning.EventBody))
	run.limitPressureWarnings[warning.Stage] = true
}
