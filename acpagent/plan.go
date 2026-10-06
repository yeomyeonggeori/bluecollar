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
	switch {
	case turnRequest.PrecomputedTurnDecision != nil:
		return plannedTurn{decision: *turnRequest.PrecomputedTurnDecision}, nil
	case turnRequest.IsRuntimeRestartResume:
		return plannedTurn{decision: withHostTaskLevel(resumedTurnDecision(openSession.recordedEvents(turnRequest), turnRequest), turnRequest)}, nil
	case isReplyToAskedQuestion(turnRequest):
		return plannedTurn{decision: withHostTaskLevel(answeredQuestionDecision(openSession.recordedEvents(turnRequest), turnRequest), turnRequest)}, nil
	}
	routed, errorValue := runningAgent.routeTurn(ctx, turnRequest)
	routed.decision = withHostTaskLevel(routed.decision, turnRequest)
	return routed, errorValue
}

func (runningAgent *Agent) routeTurn(ctx context.Context, turnRequest agentcontract.AgentTurnRequest) (plannedTurn, error) {
	router := intake.NewTurnRouter(runningAgent.languageModel, runningAgent.decisionPlanner, agentcontract.IntakeOptions{IsEnabled: true})
	callLedger := &agentcontract.IntakeCallLedger{}
	decision, errorValue := router.PlanObserved(ctx, routingRequestOf(turnRequest), callLedger)
	return plannedTurn{decision: decision, callRecords: callLedger.Records}, errorValue
}

func routingRequestOf(turnRequest agentcontract.AgentTurnRequest) agentcontract.AgentRequest {
	return agentcontract.AgentRequest{
		RequesterPersonID:    turnRequest.RequesterPersonID,
		RequesterName:        turnRequest.RequesterName,
		RequesterCallingName: turnRequest.RequesterCallingName,
		RequesterHandle:      turnRequest.RequesterHandle,
		AgentIdentity:        turnRequest.AgentIdentity,
		ConversationID:       turnRequest.ConversationID,
		ConversationType:     turnRequest.ConversationType,
		Prompt:               turnRequest.Prompt,
		InputParts:           turnRequest.InputParts,
		ResponseLanguage:     turnRequest.ResponseLanguage,
		VisibleContext:       turnRequest.VisibleContext,
		ScheduledRun:         turnRequest.ScheduledRun,
		ActiveGoal:           turnRequest.ActiveGoal,
		PriorTask:            turnRequest.PriorTask,
		TurnStartedAt:        turnRequest.TurnStartedAt,
		EnvironmentNow:       turnRequest.EnvironmentNow,
		Company:              turnRequest.Company,
		ToolSet:              turnRequest.ToolSet,
	}
}

func isReplyToAskedQuestion(turnRequest agentcontract.AgentTurnRequest) bool {
	return strings.TrimSpace(turnRequest.PendingInput.TaskRunID) != ""
}

func resumedTurnDecision(taskEvents []agentcontract.TaskEvent, turnRequest agentcontract.AgentTurnRequest) agentcontract.TurnDecision {
	decision := continuedTurnDecision(taskEvents, turnRequest, "the host resumes a run the runtime had stopped")
	decision.TaskLevel = highestRecordedTaskLevel(taskEvents)
	return decision
}

func answeredQuestionDecision(taskEvents []agentcontract.TaskEvent, turnRequest agentcontract.AgentTurnRequest) agentcontract.TurnDecision {
	return continuedTurnDecision(taskEvents, turnRequest, "a reply to the question the run asked")
}

func continuedTurnDecision(taskEvents []agentcontract.TaskEvent, turnRequest agentcontract.AgentTurnRequest, reason string) agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteContinueTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		ResponseLanguage: turnRequest.ResponseLanguage,
		Reason:           reason,
	}.WithRestoredIntakeState(latestIntakeDecision(taskEvents))
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
