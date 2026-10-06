package loop

import (
	"context"
	"encoding/json"
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
