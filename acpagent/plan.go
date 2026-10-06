package acpagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type plannedTurn struct {
	decision    agentcontract.TurnDecision
	callRecords []agentcontract.LLMCallRecord
}

func (runningAgent *Agent) planTurn(ctx context.Context, openSession *session, turnRequest agentcontract.AgentTurnRequest) (plannedTurn, error) {
	if decision, isFromFacts := DecisionFromFacts(openSession.recordedEvents(turnRequest), turnRequest); isFromFacts {
		return plannedTurn{decision: decision}, nil
	}
	routed, errorValue := runningAgent.routeTurn(ctx, turnRequest)
	routed.decision = withHostTaskLevel(routed.decision, turnRequest)
	return routed, errorValue
}

func DecisionFromFacts(taskEvents []agentcontract.TaskEvent, turnRequest agentcontract.AgentTurnRequest) (agentcontract.TurnDecision, bool) {
	switch {
	case turnRequest.IsRuntimeRestartResume:
		return withHostTaskLevel(resumedTurnDecision(taskEvents, turnRequest), turnRequest), true
	case isReplyToAskedQuestion(turnRequest):
		return withHostTaskLevel(answeredQuestionDecision(turnRequest), turnRequest), true
	}
	return agentcontract.TurnDecision{}, false
}

func (runningAgent *Agent) routeTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (plannedTurn, error) {
	router := intake.NewTurnRouter(runningAgent.languageModel, runningAgent.decisionPlanner, agentcontract.IntakeOptions{IsEnabled: true})
	callLedger := &agentcontract.IntakeCallLedger{SchemaNames: agentcontract.IntakeSchemaNames}
	decision, errorValue := router.PlanObserved(ctx, turnRequest.RoutingRequest(), agentcontract.Routing{}, callLedger)
	return plannedTurn{decision: decision, callRecords: callLedger.Records}, errorValue
}

func isReplyToAskedQuestion(turnRequest agentcontract.AgentTurnRequest) bool {
	return strings.TrimSpace(turnRequest.PendingInput.TaskRunID) != ""
}

func resumedTurnDecision(taskEvents []agentcontract.TaskEvent, turnRequest agentcontract.AgentTurnRequest) agentcontract.TurnDecision {
	decision := continuedTurnDecision(turnRequest, "the host resumes a run the runtime had stopped").WithRestoredIntakeState(latestIntakeDecision(taskEvents))
	decision.TaskLevel = highestRecordedTaskLevel(taskEvents)
	return decision
}

func answeredQuestionDecision(turnRequest agentcontract.AgentTurnRequest) agentcontract.TurnDecision {
	return continuedTurnDecision(turnRequest, "a reply to the question the run asked")
}

func continuedTurnDecision(turnRequest agentcontract.AgentTurnRequest, reason string) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteContinueTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		ResponseLanguage: turnRequest.ResponseLanguage,
		Reason:           reason,
	}
}

func withHostTaskLevel(decision agentcontract.TurnDecision, turnRequest agentcontract.AgentTurnRequest) agentcontract.TurnDecision {
	if hostTaskLevel := agentcontract.NormalizeTaskLevel(string(turnRequest.TaskLevel)); hostTaskLevel != "" {
		decision.TaskLevel = hostTaskLevel
	}
	return decision
}

func (openSession *session) recordedEvents(turnRequest agentcontract.AgentTurnRequest) []agentcontract.TaskEvent {
	taskRunID := strings.TrimSpace(turnRequest.ExistingTaskRunID)
	if taskRunID == "" {
		return nil
	}
	return openSession.taskRuns.ListTaskEvent(taskRunID)
}

func latestIntakeDecision(taskEvents []agentcontract.TaskEvent) agentcontract.IntakeDecision {
	for index := len(taskEvents) - 1; index >= 0; index-- {
		if taskEvents[index].Name != agentcontract.TaskEventAgentIntake {
			continue
		}
		var decision agentcontract.IntakeDecision
		if json.Unmarshal([]byte(taskEvents[index].Body), &decision) == nil {
			return decision
		}
	}
	return agentcontract.IntakeDecision{}
}

func highestRecordedTaskLevel(taskEvents []agentcontract.TaskEvent) agentcontract.TaskLevel {
	taskLevel := agentcontract.TaskLevelLow
	for _, taskEvent := range taskEvents {
		if taskEvent.Name != agentcontract.TaskEventAgentIntake {
			continue
		}
		var recorded struct {
			Level          string `json:"level"`
			NewTaskLevel   string `json:"newTaskLevel"`
			EffortLevel    string `json:"effortLevel"`
			NewEffortLevel string `json:"newEffortLevel"`
			TaskComplexity string `json:"taskComplexity"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &recorded) != nil {
			continue
		}
		for _, level := range []string{recorded.Level, recorded.NewTaskLevel, recorded.EffortLevel, recorded.NewEffortLevel, recorded.TaskComplexity} {
			taskLevel = agentcontract.LargerTaskLevel(taskLevel, agentcontract.NormalizeTaskLevel(level))
		}
	}
	return taskLevel
}

func NewToolSelector(decisionModel model.DecisionModel) agentcontract.ToolSelector {
	return intake.NewDecisionPlanner(decisionModel, nil)
}

func (openSession *session) recordPlanningCalls(taskRunID string, callRecords []agentcontract.LLMCallRecord) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	for _, callRecord := range callRecords {
		openSession.taskEvents.AppendLLMCall(taskRunID, callRecord)
	}
}
