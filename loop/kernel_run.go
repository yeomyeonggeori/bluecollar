package loop

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type kernelRun struct {
	kernel                    *AgentKernel
	responseContext           context.Context
	routing                   turnclassification.Routing
	requestReceivedAt         time.Time
	routerCallLedger          *intakeCallLedger
	request                   AgentRequest
	intakeRequest             AgentRequest
	turnToolSet               *toolcontract.ToolSet
	turnDecision              TurnDecision
	intakeDecision            IntakeDecision
	turnOptions               TurnOptions
	taskBudget                turnBudgetContext
	taskContext               context.Context
	baseInstructionBundle     InstructionBundle
	instructionBundle         InstructionBundle
	manualPinnedToolNames     []string
	persistedPinnedToolNames  []string
	startsNewSemanticRun      bool
	confirmationPlan          confirmationGatePlan
	confirmationEvidenceHints []string
	outcomeContract           OutcomeContract
	requiredNextToolNames     []string
}

func (agentKernel *AgentKernel) RunAgentRequest(responseContext context.Context, routing turnclassification.Routing, request AgentRequest) (AgentTurnResult, error) {
	run := &kernelRun{
		kernel:            agentKernel,
		responseContext:   responseContext,
		routing:           routing,
		requestReceivedAt: time.Now(),
		routerCallLedger:  &intakeCallLedger{},
		request:           request,
	}
	if outcome := run.routeRequest(); outcome.isFinished {
		return outcome.result, outcome.err
	}
	run.openBudget()
	defer run.taskBudget.cancel()
	stages := []func() turnOutcome{run.resolveRequest, run.chooseInstructions, run.confirmPlan, run.draftContract}
	for _, stage := range stages {
		if outcome := stage(); outcome.isFinished {
			return outcome.result, outcome.err
		}
	}
	return run.executeTurn()
}

func (run *kernelRun) routeRequest() turnOutcome {
	run.request.ActiveGoal = normalizePersistedActiveGoal(run.request.ActiveGoal)
	if run.request.TurnStartedAt.IsZero() {
		run.request.TurnStartedAt = run.requestReceivedAt.Add(-2 * time.Second)
	}
	run.request.ResponseLanguage = ResolveResponseLanguage(run.request.ResponseLanguage, run.request.VisibleContext.ResponseLanguage)
	if strings.TrimSpace(run.request.ActiveGoal.RestoreError) != "" {
		return finishedWith(run.kernel.CompleteLaunchFailure(run.responseContext, launchFailureRequest(run.request), "restore_state", "active_goal", errors.New(run.request.ActiveGoal.RestoreError)), nil)
	}
	run.baseInstructionBundle = run.kernel.currentInstructionBundle()
	run.instructionBundle = run.baseInstructionBundle
	run.turnToolSet = run.request.ToolSet
	run.intakeRequest = run.request
	turnDecision, errorValue := routedTurnDecision(run.routing)
	if errorValue != nil {
		return finishedWith(run.kernel.completeTurnRouterFailure(run.responseContext, run.intakeRequest, errorValue, run.routerCallLedger.Records), nil)
	}
	run.turnDecision = turnDecision
	run.intakeDecision = promoteArtifactTaskLevelForRequest(run.routing, run.intakeRequest, turnDecision.IntakeDecision())
	run.turnOptions = run.kernel.turnOptionsForIntakeDecision(run.responseContext, run.intakeDecision)
	return keepGoing
}

func (run *kernelRun) openBudget() {
	run.taskBudget = newTurnBudgetContext(run.responseContext, executionBudgetStartedAt(run.request), run.request.IsRuntimeRestartResume, run.requestReceivedAt, run.turnOptions)
	run.taskContext = run.taskBudget.workContext
	run.request.TurnStartedAt = run.taskBudget.turnStartedAt
}

func (run *kernelRun) stopIfIntakeElapsed() turnOutcome {
	result, didExpire := run.kernel.completeIntakeIfElapsed(run.taskBudget, run.routing, run.intakeRequest, run.intakeDecision, run.turnDecision.Route, run.routerCallLedger.Records)
	if !didExpire {
		return keepGoing
	}
	return finishedWith(result, nil)
}

func (run *kernelRun) resolveRequest() turnOutcome {
	if outcome := run.stopIfIntakeElapsed(); outcome.isFinished {
		return outcome
	}
	run.request.ResponseLanguage = ResolveResponseLanguage(run.intakeDecision.ResponseLanguage, run.request.ResponseLanguage)
	run.manualPinnedToolNames = append([]string{}, run.request.PinnedToolNames...)
	run.request = restorePersistedToolSelection(run.request)
	run.persistedPinnedToolNames = append([]string{}, run.request.PinnedToolNames...)
	run.intakeRequest = run.request
	if run.turnDecision.Route == TurnRouteStartTask {
		run.startFreshTask()
	}
	run.applyTaskLifecycle()
	run.seedToolNames()
	return run.stopIfConsumed()
}

func (run *kernelRun) startFreshTask() {
	if strings.TrimSpace(run.request.ExistingTaskRunID) == strings.TrimSpace(run.request.ActiveGoal.TaskRunID) {
		run.forgetExistingTaskRun()
	}
	run.clearActiveGoal()
	run.request, run.intakeDecision = applyPriorTaskOutcomeRecovery(run.request, run.intakeDecision)
	run.intakeDecision.InitialToolNames = registeredToolNamesOnly(run.turnToolSet, run.intakeDecision.InitialToolNames)
	run.intakeRequest.ActiveGoal = run.request.ActiveGoal
}

func (run *kernelRun) applyTaskLifecycle() {
	lifecycleMode := taskLifecycleModeForRequest(run.turnDecision, run.request)
	if lifecycleMode == taskLifecycleSemanticRevision {
		run.clearActiveGoal()
		if !run.request.IsTaskRunOpenedForThisTurn {
			run.forgetExistingTaskRun()
		}
	}
	run.startsNewSemanticRun = lifecycleMode == taskLifecycleFresh || lifecycleMode == taskLifecycleSemanticRevision
}

func (run *kernelRun) clearActiveGoal() {
	run.request.ActiveGoal = ActiveGoal{}
	run.intakeRequest.ActiveGoal = ActiveGoal{}
}

func (run *kernelRun) forgetExistingTaskRun() {
	run.request.ExistingTaskRunID = ""
	run.request.IsRuntimeRestartResume = false
	run.intakeRequest.ExistingTaskRunID = ""
	run.intakeRequest.IsRuntimeRestartResume = false
}

func (run *kernelRun) seedToolNames() {
	run.request.LikelyToolNames = appendUniqueStrings(nil, run.intakeDecision.InitialToolNames...)
	run.request.PinnedToolNames = appendUniqueStrings(append([]string{}, run.request.PinnedToolNames...), run.intakeDecision.InitialToolNames...)
	run.intakeRequest.PinnedToolNames = run.request.PinnedToolNames
	run.intakeRequest.LikelyToolNames = run.request.LikelyToolNames
}

func (run *kernelRun) stopIfConsumed() turnOutcome {
	if run.turnDecision.Route != TurnRouteConsume {
		return keepGoing
	}
	result, errorValue := run.kernel.completeConsumedRequest(run.intakeRequest, run.turnDecision, run.routerCallLedger.Records)
	return finishedWith(result, errorValue)
}

func (run *kernelRun) chooseInstructions() turnOutcome {
	if !run.request.SkipSkillSelection {
		run.instructionBundle, run.intakeDecision = run.kernel.selectInstructionBundleForResolvedRequest(run.taskContext, run.baseInstructionBundle, run.request, run.intakeDecision)
	}
	if run.instructionBundle.ContractSkillArbitrationFailed {
		run.kernel.taskRunService.AppendTaskEvent(run.request.ExistingTaskRunID, agentcontract.TaskEventAgentContractArbitrationDegraded, marshalEventBody(map[string]string{
			"reason": "contract skill arbitration failed; continuing with score-selected skills",
		}))
	}
	if outcome := run.stopIfIntakeElapsed(); outcome.isFinished {
		return outcome
	}
	run.pinToolsForEvidence(run.instructionBundle.RequiredEvidenceTools)
	run.request.PinnedSkillNames = appendUniqueStrings(run.request.PinnedSkillNames, selectedSkillNameList(run.instructionBundle.SkillDecisions)...)
	run.intakeRequest.PinnedSkillNames = run.request.PinnedSkillNames
	return run.stopIfIntakeOnly()
}

func (run *kernelRun) pinToolsForEvidence(requiredEvidenceTools []string) {
	run.request.PinnedToolNames = pinnedToolNamesForResolvedRequest(
		run.manualPinnedToolNames,
		run.persistedPinnedToolNames,
		run.intakeDecision.InitialToolNames,
		requiredEvidenceTools,
		run.startsNewSemanticRun,
	)
	run.intakeRequest.PinnedToolNames = run.request.PinnedToolNames
}

func intakeOnlyStatus(classification agentcontract.IntakeClassification) (agentcontract.TaskStatus, bool) {
	switch classification {
	case IntakeClassificationNeedsConfirmation:
		return agentcontract.TaskStatusWaitingUserInput, true
	case IntakeClassificationUnsupported:
		return agentcontract.TaskStatusBlocked, true
	}
	return "", false
}

func (run *kernelRun) stopIfIntakeOnly() turnOutcome {
	status, isIntakeOnly := intakeOnlyStatus(run.intakeDecision.Classification)
	if !isIntakeOnly {
		return keepGoing
	}
	result, errorValue := run.kernel.completeIntakeOnlyRequest(run.taskContext, run.routing, run.intakeRequest, run.intakeDecision, status, run.routerCallLedger.Records)
	result.TurnRoute = run.turnDecision.Route
	return finishedWith(result, errorValue)
}

func (run *kernelRun) applyRequiredNextTools() {
	run.requiredNextToolNames = requiredNextToolNamesForResolvedRequest(run.request.ActiveGoal, run.instructionBundle.RequiredNextTools)
	run.request.ActiveGoal.RequiredNextTools = run.requiredNextToolNames
}

func (run *kernelRun) confirmPlan() turnOutcome {
	run.applyRequiredNextTools()
	run.intakeRequest.ActiveGoal = run.request.ActiveGoal
	evidenceHints := selectedEvidenceHintTools(run.instructionBundle)
	run.confirmationEvidenceHints = confirmationEvidenceHintsForRequest(run.request, run.intakeDecision, evidenceHints)
	confirmationPlan, errorValue := run.kernel.planConfirmationGate(run.taskContext, run.request, run.intakeDecision, run.confirmationEvidenceHints)
	if errorValue != nil {
		if outcome := run.stopIfIntakeElapsed(); outcome.isFinished {
			return outcome
		}
		return finishedWith(AgentTurnResult{}, errorValue)
	}
	run.confirmationPlan = confirmationPlan
	if confirmationPlan.DegradedError != nil {
		run.kernel.taskRunService.AppendTaskEvent(strings.TrimSpace(run.request.ExistingTaskRunID), agentcontract.TaskEventAgentConfirmationPlanDegraded, marshalEventBody(map[string]string{
			"reason": "execution plan generation failed twice; continuing with the runtime tool approval gate: " + confirmationPlan.DegradedError.Error(),
		}))
	}
	if confirmationPlan.Decision.RequiresClarification {
		return run.pauseForClarification()
	}
	return keepGoing
}

func (run *kernelRun) pauseForClarification() turnOutcome {
	result, pauseError := run.kernel.pauseForClarification(run.taskContext, run.request, run.intakeDecision, run.confirmationPlan, OutcomeContract{}, run.confirmationEvidenceHints, selectedSkillNameList(run.instructionBundle.SkillDecisions))
	if pauseError != nil && run.taskBudget.didWorkExpire() {
		run.intakeRequest.ExistingTaskRunID = result.TaskRun.TaskRunID
		result = run.kernel.completeIntakeElapsed(run.taskBudget, run.routing, run.intakeRequest, run.intakeDecision, run.routerCallLedger.Records)
		pauseError = nil
	}
	result.TurnRoute = run.turnDecision.Route
	return finishedWith(result, pauseError)
}

func (run *kernelRun) draftContract() turnOutcome {
	plan := run.confirmationPlan
	requiredAttachmentSuffixes := attachmentSuffixesForRequestedOutputFormats(run.intakeDecision.RequestedOutputFormats)
	contract := outcomeContractForRequest(run.request, run.intakeDecision, run.instructionBundle, plan.ExecutionPlan, plan.HasExecutionPlan, requiredAttachmentSuffixes)
	contract = dischargeResolvedInputContract(run.request, run.turnDecision, contract)
	run.outcomeContract = contractReducedToCallableTools(run.request.ToolSet, contract)
	if outcome := run.stopIfIntakeElapsed(); outcome.isFinished {
		return outcome
	}
	run.applyRequiredNextTools()
	run.pinToolsForEvidence(run.outcomeContract.RequiredEvidenceTools)
	return keepGoing
}
