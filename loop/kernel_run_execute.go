package loop

import (
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func (run *kernelRun) executeTurn() (AgentTurnResult, error) {
	turnRequest := run.turnRequest()
	agentTurnRunner := run.newTurnRunner()
	result, errorValue := agentTurnRunner.RunTurn(run.taskBudget.callerContext(), turnRequest)
	result.TurnRoute = run.turnDecision.Route
	result.ToolNames = toolNamesForEvent(turnRequest.ToolSet)
	if result.TaskRun.TaskRunID != "" {
		run.recordTurnEvents(result, turnRequest)
	}
	return result, errorValue
}

func (run *kernelRun) newTurnRunner() *AgentTurnRunner {
	kernel := run.kernel
	agentTurnRunner := NewAgentTurnRunnerWithRecoveryModel(
		kernel.taskRunService,
		kernel.taskStepService,
		kernel.taskArtifactService,
		kernel.taskLanguageModelForLevel(run.intakeDecision.TaskLevel),
		kernel.languageModel,
		run.turnOptions,
	)
	agentTurnRunner.UseIterationCostObserver(kernel.iterationCostObserver)
	agentTurnRunner.UseToolSelector(kernel.toolSelector)
	agentTurnRunner.UseToolResultSpillStore(kernel.toolResultSpillStore)
	agentTurnRunner.UseToolResultImageSource(kernel.toolResultImageSource)
	agentTurnRunner.UseDecisionModel(kernel.decisionModel)
	return agentTurnRunner
}

func (run *kernelRun) recordTurnEvents(result AgentTurnResult, turnRequest AgentTurnRequest) {
	taskRunID := result.TaskRun.TaskRunID
	run.kernel.appendTurnRouterCallRecords(taskRunID, run.routerCallLedger.Records)
	run.kernel.taskRunService.AppendTaskEvent(taskRunID, agentcontract.TaskEventAgentIntake, marshalEventBody(run.intakeDecision))
	run.kernel.appendCompanyTimeZoneFallbackEvent(taskRunID, turnRequest.Company)
	run.kernel.appendGoalLifecycleEvent(result.TaskRun, turnRequest.ActiveGoal)
}

func (run *kernelRun) turnRequest() AgentTurnRequest {
	request := run.request
	instructionBundle := run.instructionBundle
	plan := run.confirmationPlan
	return AgentTurnRequest{
		RequesterPersonID:          request.RequesterPersonID,
		Company:                    run.kernel.companyContext(),
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
		ToolSet:                    run.turnToolSet,
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
		ContractToolWorkingSet:     run.contractToolWorkingSet(),
		RequiredEvidenceTools:      run.outcomeContract.RequiredEvidenceTools,
		RequiredAttachmentSuffixes: run.outcomeContract.RequiredAttachmentSuffixes,
		OutcomeContract:            run.outcomeContract,
		ActiveGoal:                 activeGoalForTurn(request, run.outcomeContract, plan.ExecutionPlan, plan.HasExecutionPlan),
		PriorTask:                  request.PriorTask,
		ScheduledRun:               request.ScheduledRun,
		TaskShape:                  run.intakeDecision.TaskShape,
		TaskLevel:                  run.intakeDecision.TaskLevel,
		TurnStartedAt:              request.TurnStartedAt,
		EnvironmentNow:             request.EnvironmentNow,
		EffortStartedAt:            request.TurnStartedAt,
		TurnAnchorClamped:          run.taskBudget.didClampAnchor,
		OriginalTurnStartedAt:      run.taskBudget.originalTurnStartedAt,
		CarriedOutCalls:            request.CarriedOutCalls,
		CheckpointSender:           request.CheckpointSender,
		TaskRunChosen:              request.TaskRunChosen,
	}
}

func (run *kernelRun) contractToolWorkingSet() ContractToolWorkingSet {
	return ContractToolWorkingSet{
		RequiredNextTools:     run.requiredNextToolNames,
		RequiredEvidenceTools: append([]string{}, run.instructionBundle.RequiredEvidenceTools...),
	}
}
