package loop

import (
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

const deletionReply = "오래된 작업을 삭제했습니다."

type deletionReplyLanguageModel struct {
	stubStructuredLanguageModel
}

func (languageModel *deletionReplyLanguageModel) GenerateChatCompletion(context.Context, model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return model.ChatCompletionResponse{
		FinishReason: "stop",
		Message:      model.ChatCompletionMessage{Role: "assistant", Content: deletionReply},
	}, nil
}

func servicesStoppingAfterDeletion(expectedChanges string, decisionModel model.DecisionModel) turnRunnerTestServices {
	languageModel := &deletionReplyLanguageModel{stubStructuredLanguageModel{contents: []string{expectedChanges}}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	services.runner.UseDecisionModel(decisionModel)
	return services
}

func stopAfterDeletion(services turnRunnerTestServices, state agentTaskState) (AgentTurnResult, bool, string) {
	request := deleteRequest(taskDeleteToolSet())
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)
	state.Observations = []turnObservation{deletedTaskObservation()}
	result, isCompleted := services.runner.completeAtStop(context.Background(), taskRun.TaskRunID, request, &state)
	return result, isCompleted, taskRun.TaskRunID
}

func TestAStopCompletesWhenTheAskedChangesAreConfirmed(t *testing.T) {
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9}})

	result, isCompleted, _ := stopAfterDeletion(services, agentTaskState{})

	if !isCompleted || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("a stop after every asked change was recorded is a finished task, got %+v", result)
	}
	if result.FinishMessage != deletionReply {
		t.Fatalf("expected the written completion reply, got %q", result.FinishMessage)
	}
}

func TestAStopDoesNotCompleteWhileAnAskedChangeIsUnmet(t *testing.T) {
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}})

	_, isCompleted, taskRunID := stopAfterDeletion(services, agentTaskState{CompletionIntentToolName: "task_delete"})

	if isCompleted {
		t.Fatal("the model saying it was done does not outweigh a change the record says is not done")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRunID), "completion.change_check", `"unmet":[{"change":"task deleted"`) {
		t.Fatal("expected a completion.change_check event naming the unmet change")
	}
}

func TestAStopWithAnUnavailableCheckWaitsForTheModelToSayItWasDone(t *testing.T) {
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), &scriptedDecisionModel{errorValue: errors.New("decisions unavailable")})

	if _, isCompleted, _ := stopAfterDeletion(services, agentTaskState{}); isCompleted {
		t.Fatal("with the check unavailable and no claim from the model, nothing says the task is done")
	}
}

func TestAStopWithAnUnavailableCheckCompletesWhatTheModelSaidWasDone(t *testing.T) {
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), &scriptedDecisionModel{errorValue: errors.New("decisions unavailable")})

	result, isCompleted, taskRunID := stopAfterDeletion(services, agentTaskState{CompletionIntentToolName: "task_delete"})

	if !isCompleted || result.FinishMessage != deletionReply {
		t.Fatalf("an unavailable check falls back to the model's own claim, as a finish does, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRunID), "completion.check_degraded", "") {
		t.Fatal("expected a completion.check_degraded event to be recorded")
	}
}

func TestAStopWithNothingAskedToChangeWaitsForTheModelToSayItWasDone(t *testing.T) {
	services := servicesStoppingAfterDeletion(expectedChangesDocument(), &scriptedDecisionModel{})

	if _, isCompleted, _ := stopAfterDeletion(services, agentTaskState{}); isCompleted {
		t.Fatal("a request that asked for no change is answered by the model, and a stop it never claimed is not an answer")
	}
	if _, isCompleted, _ := stopAfterDeletion(services, agentTaskState{CompletionIntentToolName: "task_delete"}); !isCompleted {
		t.Fatal("a request that asked for no change completes once the model says its last step was the answer")
	}
}
