package loop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestAnUnmetChangeOnlyTheRequesterCanSupplyEndsTheTurnWithAReplyAskingForIt(t *testing.T) {
	languageModel := &refinishingLanguageModel{}
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}, choice: map[string]string{"gap0": changeGapRequesterInformation}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8})
	services.runner.UseDecisionModel(decisionModel)
	request := deleteRequest(taskDeleteToolSet())
	request.RequesterPersonID = "person-1"
	request.ConversationID = "conversation-1"
	request.PinnedToolNames = request.ToolSet.ListToolNames()

	result, errorValue := services.runner.RunTurn(context.Background(), request)

	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the run completed at its first finish, got %v %+v", errorValue, result.TaskRun)
	}
	if languageModel.actionCount != 2 {
		t.Fatalf("expected no work sent back after the first finish, got %d actions", languageModel.actionCount)
	}
	if len(languageModel.unmetReplyPrompts) != 1 {
		t.Fatalf("expected the reply rewritten once, got %q", languageModel.unmetReplyPrompts)
	}
	prompt := languageModel.unmetReplyPrompts[0]
	if !strings.Contains(prompt, "need information the person has not given") || strings.Contains(prompt, "It was written as though every asked change were done") {
		t.Fatalf("expected the rewrite to ask the person for what is missing and nothing else, got %q", prompt)
	}
}

func TestAnUnmetChangeTheWorkCanStillDoGoesBackToTheWork(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}, choice: map[string]string{"gap0": changeGapWorkLeft}}
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), decisionModel)
	request := deleteRequest(taskDeleteToolSet())
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	result := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, []turnObservation{deletedTaskObservation()})

	if result.IsSatisfied || !result.IsChangeCheckUnmet {
		t.Fatalf("expected work left sent back to the loop, got %+v", result)
	}
}

func TestAChangeAwaitingTheRequesterDoesNotEndATurnThatStillHasWorkLeft(t *testing.T) {
	second := expectedChange{Change: deleteOldTask.Change, Asked: "두 번째 오래된 작업도 삭제해줘"}
	decisionModel := &scriptedDecisionModel{
		noul:   map[string]float64{"expected0": 0.1, "expected1": 0.1},
		choice: map[string]string{"gap0": changeGapRequesterInformation, "gap1": changeGapWorkLeft},
	}
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask, second), decisionModel)
	request := deleteRequest(taskDeleteToolSet())
	request.Prompt = deleteOldTask.Asked + ". " + second.Asked
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	result := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, []turnObservation{deletedTaskObservation()})

	if result.IsSatisfied {
		t.Fatalf("expected the turn kept going while work is left, got %+v", result)
	}
	if !strings.Contains(result.Message, "only the requester can give") {
		t.Fatalf("expected the loop told which change awaits the requester, got %q", result.Message)
	}
}

func TestTheGapIsAskedOnlyWhenAChangeIsFoundUnmet(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9}}
	request := deleteRequest(taskDeleteToolSet())

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{deleteOldTask}, []turnObservation{deletedTaskObservation()})

	if len(decisionModel.requests) != 1 {
		t.Fatalf("expected one decision call when every change was carried out, got %d", len(decisionModel.requests))
	}
}

func TestAGapCheckThatFailsLeavesTheChangeAsWorkLeft(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}, gapError: errors.New("decision endpoint returned 503")}
	request := deleteRequest(taskDeleteToolSet())

	check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{deleteOldTask}, []turnObservation{deletedTaskObservation()})

	if errorValue != nil || check.awaitsOnlyTheRequester() || check.GapCheckError == "" {
		t.Fatalf("expected the change left as work and the failure recorded, got %+v %v", check, errorValue)
	}
}
