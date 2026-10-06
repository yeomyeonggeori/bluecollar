package routedharness

import (
	"context"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type Harness struct {
	Inner    agentcontract.Harness
	taskRuns taskstate.TaskRunStore
	router   intake.TurnRouter
}

func New(inner agentcontract.Harness, taskRuns taskstate.TaskRunStore, languageModel model.LanguageModelProvider, decisionModel model.DecisionModel) Harness {
	router := intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(decisionModel, nil), agentcontract.IntakeOptions{IsEnabled: true})
	return Harness{Inner: inner, taskRuns: taskRuns, router: router}
}

func (routed Harness) RunTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	if turnRequest.PrecomputedTurnDecision == nil {
		decision, isFromFacts := acpagent.DecisionFromFacts(routed.recordedEvents(turnRequest), turnRequest)
		if isFromFacts {
			turnRequest.PrecomputedTurnDecision = &decision
			return routed.Inner.RunTurn(ctx, turnRequest)
		}
		decision, errorValue := routed.router.Plan(ctx, turnRequest.RoutingRequest())
		if errorValue != nil {
			return agentcontract.AgentTurnResult{}, errorValue
		}
		turnRequest.PrecomputedTurnDecision = &decision
	}
	return routed.Inner.RunTurn(ctx, turnRequest)
}

func (routed Harness) recordedEvents(turnRequest agentcontract.AgentTurnRequest) []agentcontract.TaskEvent {
	taskRunID := strings.TrimSpace(turnRequest.ExistingTaskRunID)
	if taskRunID == "" || routed.taskRuns == nil {
		return nil
	}
	return routed.taskRuns.ListTaskEvent(taskRunID)
}
