package loop

import (
	"context"
	"errors"
	"time"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

const nonResumeAnchorStaleAllowance = 2 * time.Minute

type turnBudgetContext struct {
	parentContext         context.Context
	totalContext          context.Context
	workContext           context.Context
	cancelTotal           context.CancelFunc
	cancelWork            context.CancelFunc
	turnOptions           TurnOptions
	turnStartedAt         time.Time
	workDeadline          time.Time
	didClampAnchor        bool
	originalTurnStartedAt time.Time
}

func clampedTurnStartedAt(turnStartedAt time.Time, isRuntimeRestartResume bool, referenceNow time.Time) (resolvedTurnStartedAt time.Time, didClampAnchor bool, originalTurnStartedAt time.Time) {
	if isRuntimeRestartResume || turnStartedAt.IsZero() {
		return turnStartedAt, false, turnStartedAt
	}
	if referenceNow.Sub(turnStartedAt) <= nonResumeAnchorStaleAllowance {
		return turnStartedAt, false, turnStartedAt
	}
	return referenceNow, true, turnStartedAt
}

func newTurnBudgetContext(parentContext context.Context, turnStartedAt time.Time, isRuntimeRestartResume bool, referenceNow time.Time, turnOptions TurnOptions) turnBudgetContext {
	resolvedTurnStartedAt, didClampAnchor, originalTurnStartedAt := clampedTurnStartedAt(turnStartedAt, isRuntimeRestartResume, referenceNow)
	if resolvedTurnStartedAt.IsZero() || turnOptions.MaxElapsedSecond <= 0 {
		totalContext, cancelTotal := context.WithCancel(parentContext)
		workContext, cancelWork := context.WithCancel(totalContext)
		return turnBudgetContext{
			parentContext:         parentContext,
			totalContext:          totalContext,
			workContext:           workContext,
			cancelTotal:           cancelTotal,
			cancelWork:            cancelWork,
			turnOptions:           turnOptions,
			turnStartedAt:         resolvedTurnStartedAt,
			didClampAnchor:        didClampAnchor,
			originalTurnStartedAt: originalTurnStartedAt,
		}
	}
	totalDuration := time.Duration(turnOptions.MaxElapsedSecond) * time.Second
	workDeadline := resolvedTurnStartedAt.Add(workDurationWithinTotal(totalDuration))
	totalContext, cancelTotal := context.WithDeadline(parentContext, resolvedTurnStartedAt.Add(totalDuration))
	workContext, cancelWork := context.WithDeadline(totalContext, workDeadline)
	return turnBudgetContext{
		parentContext:         parentContext,
		totalContext:          totalContext,
		workContext:           workContext,
		cancelTotal:           cancelTotal,
		cancelWork:            cancelWork,
		turnOptions:           turnOptions,
		turnStartedAt:         resolvedTurnStartedAt,
		workDeadline:          workDeadline,
		didClampAnchor:        didClampAnchor,
		originalTurnStartedAt: originalTurnStartedAt,
	}
}

func (turnBudget turnBudgetContext) cancel() {
	turnBudget.cancelWork()
	turnBudget.cancelTotal()
}

func (turnBudget turnBudgetContext) callerContext() context.Context {
	return turnBudget.parentContext
}

func (turnBudget turnBudgetContext) didWorkExpire() bool {
	return turnBudget.parentContext.Err() == nil && errors.Is(turnBudget.workContext.Err(), context.DeadlineExceeded)
}

func (agentKernel *AgentKernel) completeIntakeIfElapsed(turnBudget turnBudgetContext, routing turnclassification.Routing, request AgentRequest, intakeDecision IntakeDecision, turnRoute TurnRoute, routerCallRecords []llmCallRecord) (AgentTurnResult, bool) {
	if !turnBudget.didWorkExpire() {
		return AgentTurnResult{}, false
	}
	result := agentKernel.completeIntakeElapsed(turnBudget, routing, request, intakeDecision, routerCallRecords)
	result.TurnRoute = turnRoute
	return result, true
}

func (agentKernel *AgentKernel) completeIntakeElapsed(turnBudget turnBudgetContext, routing turnclassification.Routing, request AgentRequest, intakeDecision IntakeDecision, routerCallRecords []llmCallRecord) AgentTurnResult {
	taskRun := agentKernel.taskRunForRequest(request)
	agentKernel.appendTurnRouterCallRecords(taskRun.TaskRunID, routerCallRecords)
	if intakeDecision.TaskLevel != "" {
		agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentIntake, marshalEventBody(intakeDecision))
	}
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLimitStop, marshalEventBody(intakeLimitEventBody(turnBudget)))
	if turnBudget.didClampAnchor {
		agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentTurnAnchorClamped, marshalEventBody(turnAnchorClampedEventBody(turnBudget)))
	}
	blockedTaskRun, errorValue := agentKernel.taskRunService.PauseTaskRun(taskRun.TaskRunID, agentcontract.TaskStatusBlocked, "max_elapsed")
	if errorValue != nil {
		taskRun.Status = agentcontract.TaskStatusBlocked
		taskRun.FailureReason = "max_elapsed"
		blockedTaskRun = taskRun
	}
	failureReport := buildIntakeFailureReport(turnBudget, request, intakeDecision, taskRun.TaskRunID)
	failureNotice, noticeStatus := agentKernel.generateIntakeElapsedNotice(turnBudget.totalContext, failureReport)
	replyStatus := limitReplyStatus{Source: noticeStatus.Source, Reason: noticeStatus.Reason, TextRecoveryError: noticeStatus.TextRecoveryError}
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentLimitReply, marshalEventBody(replyStatus))
	agentKernel.taskRunService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventAgentFailureReport, marshalEventBody(failureReportEventBody("limit", failureReport, noticeStatus)))
	blockedTaskRun = persistTaskRunResult(agentKernel.taskRunService, blockedTaskRun, failureNotice.SendableMessage())
	agentKernel.appendGoalLifecycleEvent(blockedTaskRun, activeGoalFromIntakeOnly(routing, taskRun.TaskRunID, request, intakeDecision, agentcontract.TaskStatusBlocked))
	return AgentTurnResult{
		TaskRun:       blockedTaskRun,
		UserNotice:    failureNotice.SendableMessage(),
		FailureNotice: failureNotice,
		ToolNames:     toolNamesForEvent(request.ToolSet),
	}
}

func (agentKernel *AgentKernel) generateIntakeElapsedNotice(responseContext context.Context, report FailureReport) (FailureNotice, FailureNoticeGenerationStatus) {
	return (FailureNoticeGenerator{LanguageModel: agentKernel.languageModel}).Generate(responseContext, report)
}

func intakeLimitEventBody(turnBudget turnBudgetContext) map[string]any {
	turnOptions := turnBudget.turnOptions
	body := map[string]any{
		"phase":              "intake",
		"taskLevel":          turnOptions.TaskLevel,
		"maxIterationCount":  turnOptions.MaxIterationCount,
		"maxElapsedSecond":   turnOptions.MaxElapsedSecond,
		"maxToolCallCount":   turnOptions.MaxToolCallCount,
		"usedIterationCount": 0,
		"usedToolCallCount":  0,
		"limitStopReason":    "max_elapsed",
		"anchorClamped":      turnBudget.didClampAnchor,
		"nowUnixMs":          time.Now().UnixMilli(),
	}
	if !turnBudget.turnStartedAt.IsZero() {
		body["turnStartedAtUnixMs"] = turnBudget.turnStartedAt.UnixMilli()
	}
	if !turnBudget.workDeadline.IsZero() {
		body["workDeadlineUnixMs"] = turnBudget.workDeadline.UnixMilli()
	}
	if turnBudget.didClampAnchor {
		body["originalTurnStartedAtUnixMs"] = turnBudget.originalTurnStartedAt.UnixMilli()
	}
	return body
}

func turnAnchorClampedEventBody(turnBudget turnBudgetContext) map[string]any {
	return map[string]any{
		"phase":                       "intake",
		"maxElapsedSecond":            turnBudget.turnOptions.MaxElapsedSecond,
		"originalTurnStartedAtUnixMs": turnBudget.originalTurnStartedAt.UnixMilli(),
		"clampedTurnStartedAtUnixMs":  turnBudget.turnStartedAt.UnixMilli(),
		"nowUnixMs":                   time.Now().UnixMilli(),
	}
}

func withElapsedBudgetFromDeadline(ctx context.Context, turnOptions TurnOptions) TurnOptions {
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return turnOptions
	}
	remainingSecond := int(time.Until(deadline).Seconds())
	if remainingSecond <= 0 {
		return turnOptions
	}
	turnOptions.DeadlineSecond = remainingSecond
	if turnOptions.MaxElapsedSecond > remainingSecond {
		turnOptions.MaxElapsedSecond = remainingSecond
	}
	return turnOptions
}

func elapsedBudgetForProfile(taskLevelProfile TaskLevelProfile, throughput IterationCost) time.Duration {
	return DurationForIterationCount(taskLevelProfile.MaxIterationCount, throughput, taskLevelProfile.CostCeiling)
}
