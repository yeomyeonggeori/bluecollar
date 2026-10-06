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

type PlannedTurnRunner interface {
	RunPlannedTurn(context.Context, agentcontract.AgentTurnRequest, agentcontract.Routing) (agentcontract.AgentTurnResult, error)
}

type Harness struct {
	Inner    PlannedTurnRunner
	taskRuns taskstate.TaskRunStore
	router   intake.TurnRouter
}

func New(inner PlannedTurnRunner, taskRuns taskstate.TaskRunStore, languageModel model.LanguageModelProvider, decisionModel model.DecisionModel) Harness {
	router := intake.NewTurnRouter(languageModel, intake.NewDecisionPlanner(decisionModel, nil), agentcontract.IntakeOptions{IsEnabled: true})
	return Harness{Inner: inner, taskRuns: taskRuns, router: router}
}

func (routed Harness) RunTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (agentcontract.AgentTurnResult, error) {
	decision, isFromFacts := acpagent.DecisionFromFacts(routed.recordedEvents(turnRequest), turnRequest)
	if !isFromFacts {
		var errorValue error
		decision, errorValue = routed.router.Plan(ctx, turnRequest.RoutingRequest())
		if errorValue != nil {
			return agentcontract.AgentTurnResult{}, errorValue
		}
	}
	return routed.Inner.RunPlannedTurn(ctx, turnRequest, agentcontract.Routing{Decision: &decision})
}

func (routed Harness) recordedEvents(turnRequest agentcontract.AgentTurnRequest) []agentcontract.TaskEvent {
	taskRunID := strings.TrimSpace(turnRequest.ExistingTaskRunID)
	if taskRunID == "" || routed.taskRuns == nil {
		return nil
	}
	return routed.taskRuns.ListTaskEvent(taskRunID)
}
