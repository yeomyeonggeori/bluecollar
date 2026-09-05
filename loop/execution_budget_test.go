package loop

import (
	"context"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestExecutionBudgetStartsAfterCompletedRouting(t *testing.T) {
	agentKernel, _ := newKernelTestServices()
	agentKernel.UseLanguageModelProvider(&sequenceLanguageModel{contents: []string{finishMessageDocument("The answer is ready.")}})
	request := kernelTestRequest("Answer the question")
	request.SkipSkillSelection = true
	request.TurnStartedAt = time.Now().Add(-110 * time.Second)
	request.ExecutionStartedAt = time.Now()
	request.PrecomputedTurnDecision = &TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationQuickReply,
		TaskShape:        TaskShapeImmediateReply,
		TaskLevel:        TaskLevelXLow,
		ResponseLanguage: "en",
	}
	result, errorValue := agentKernel.RunAgentRequest(context.Background(), request)
	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("completed routing must leave an execution budget: result=%+v error=%v", result, errorValue)
	}
}

func TestExecutionBudgetPreservesCallerDeadline(t *testing.T) {
	startedAt := time.Now()
	callerDeadline := startedAt.Add(time.Second)
	parentContext, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	budget := newTurnBudgetContext(parentContext, startedAt, false, startedAt, TurnOptions{MaxElapsedSecond: 172})
	defer budget.cancel()
	deadline, hasDeadline := budget.workContext.Deadline()
	if !hasDeadline || !deadline.Equal(callerDeadline) {
		t.Fatalf("caller deadline must cap execution: %v", deadline)
	}
}

func TestExecutionBudgetKeepsRestartAndLegacyAnchors(t *testing.T) {
	startedAt := time.Now().Add(-time.Minute)
	requests := []AgentRequest{
		{TurnStartedAt: startedAt},
		{TurnStartedAt: startedAt, ExecutionStartedAt: time.Now(), IsRuntimeRestartResume: true},
	}
	for _, request := range requests {
		if !executionBudgetStartedAt(request).Equal(startedAt) {
			t.Fatalf("existing budget anchor must survive: %+v", request)
		}
	}
}
