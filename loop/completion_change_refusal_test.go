package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func TestTheChangeCheckDoesNotRefuseTheRecordedWorkbookAgainOverTheSameState(t *testing.T) {
	delivery := w1DeliveredWorkbook(t)
	asked := expectedChange{Change: "file changed", Asked: strings.SplitN(delivery.Prompt, "\n", 2)[0]}
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.22}}
	services := servicesStoppingAfterDeletion(expectedChangesDocument(asked), decisionModel)
	request := AgentTurnRequest{Prompt: delivery.Prompt, ToolSet: commandFileToolSet()}
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)
	observations := append([]turnObservation(nil), delivery.Observations...)

	refusal := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)
	observations = append(observations, completionGateObservation(len(observations)+1, refusal, request.ToolSet, observations))
	redelivery := observations[len(delivery.Observations)-1]
	redelivery.ObservationID = nextObservationIDForObservations(observations)
	observations = append(observations, redelivery)
	repeat := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)

	if refusal.IsSatisfied {
		t.Fatal("expected the first check over the recorded workbook refused")
	}
	if len(decisionModel.requests) != 1 {
		t.Fatalf("expected the same judged state asked about once, got %d decision calls", len(decisionModel.requests))
	}
	if !repeat.IsSatisfied {
		t.Fatalf("expected a refusal over the state already refused not repeated, got %+v", repeat)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "completion.change_check", `"repeatsRefusalOf":"obs-004"`) {
		t.Fatal("expected the unmet change recorded with the refusal it repeats")
	}
}

func TestTheChangeCheckJudgesAgainOnceTheStateChanges(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}}
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), decisionModel)
	request := deleteRequest(taskDeleteToolSet())
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)
	observations := []turnObservation{deletedTaskObservation()}

	refusal := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)
	observations = append(observations, completionGateObservation(len(observations)+1, refusal, request.ToolSet, observations))
	another := deletedTaskObservation()
	another.ObservationID = nextObservationIDForObservations(observations)
	another.Output.Data = []byte(`{"taskID":"task-2"}`)
	another.Effects[0].ID = "task-2"
	observations = append(observations, another)
	second := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)

	if len(decisionModel.requests) != 2 || second.IsSatisfied {
		t.Fatalf("expected a changed state judged again and refused again, got %d calls and %+v", len(decisionModel.requests), second)
	}
}

const unmetDeletionReply = "오래된 작업은 아직 삭제되지 않았습니다."

type refinishingLanguageModel struct {
	actionCount       int
	unmetReplyPrompts []string
}

func (languageModel *refinishingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *refinishingLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == expectedChangesSchemaName {
		return model.StructuredResponse{Content: expectedChangesDocument(deleteOldTask)}, nil
	}
	return model.StructuredResponse{}, nil
}

func (languageModel *refinishingLanguageModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	if request.SchemaName == unmetChangesReplySchemaName {
		languageModel.unmetReplyPrompts = append(languageModel.unmetReplyPrompts, request.Messages[0].Content)
		return model.ChatCompletionResponse{FinishReason: "stop", Message: model.ChatCompletionMessage{Role: "assistant", Content: unmetDeletionReply}}, nil
	}
	languageModel.actionCount++
	toolCall := nativeAgentActionToolCall("task_delete", `{"taskID":"task-1"}`)
	if languageModel.actionCount > 1 {
		toolCall = nativeAgentActionToolCall("reply", `{"final":true,"message":"`+deletionReply+`","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`)
	}
	return model.ChatCompletionResponse{FinishReason: "tool_calls", Message: model.ChatCompletionMessage{Role: "assistant", ToolCalls: []model.ChatCompletionToolCall{toolCall}}}, nil
}

func TestAFinishRefusedOverTheSameStateCompletesWithAReplyStatingWhatIsUnmet(t *testing.T) {
	languageModel := &refinishingLanguageModel{}
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8})
	services.runner.UseDecisionModel(decisionModel)
	request := deleteRequest(taskDeleteToolSet())
	request.RequesterPersonID = "person-1"
	request.ConversationID = "conversation-1"
	request.PinnedToolNames = request.ToolSet.ListToolNames()

	result, errorValue := services.runner.RunTurn(context.Background(), request)

	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the run to finish instead of being refused until its limit, got %v %+v", errorValue, result.TaskRun)
	}
	if len(decisionModel.requests) != 1 {
		t.Fatalf("expected the repeat recognized without asking the decision model again, got %d calls", len(decisionModel.requests))
	}
	if result.FinishMessage != unmetDeletionReply {
		t.Fatalf("expected the reply rewritten to state what is unmet, got %q", result.FinishMessage)
	}
	if len(languageModel.unmetReplyPrompts) != 1 || !strings.Contains(languageModel.unmetReplyPrompts[0], deleteOldTask.Asked) || !strings.Contains(languageModel.unmetReplyPrompts[0], deletionReply) {
		t.Fatalf("expected the rewrite asked once with the unmet change and the model's reply, got %q", languageModel.unmetReplyPrompts)
	}
}

func TestAStopDoesNotCountAStateAlreadyRefusedAsDone(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}}
	services := servicesStoppingAfterDeletion(expectedChangesDocument(deleteOldTask), decisionModel)
	request := deleteRequest(taskDeleteToolSet())
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)
	observations := []turnObservation{deletedTaskObservation()}
	refusal := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)
	state := agentTaskState{CompletionIntentToolName: "task_delete", Observations: append(observations, completionGateObservation(len(observations)+1, refusal, request.ToolSet, observations))}

	if _, isCompleted := services.runner.completeAtStop(context.Background(), taskRun.TaskRunID, request, &state); isCompleted {
		t.Fatal("expected a stop over a state the check already refused left to the limit's own reply")
	}
}
