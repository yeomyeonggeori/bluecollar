package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func silentStopResponse() model.ChatCompletionResponse {
	return model.ChatCompletionResponse{FinishReason: "stop", Message: model.ChatCompletionMessage{Role: "assistant"}}
}

func TestAModelThatAnswersWithNothingIsAskedAgainForACallAmongEveryTool(t *testing.T) {
	state := nativeAgentActionTestStateWithTools("write", toolcontract.BashToolName)
	provider := nativeAgentActionLanguageModel{chatResponses: []model.ChatCompletionResponse{
		silentStopResponse(),
		nativeAgentActionChatResponse(toolcontract.BashToolName, `{"command":"ls"}`),
	}}

	action, errorValue := DecideAgentAction(context.Background(), &provider, state)

	if errorValue != nil || action.ToolName != toolcontract.BashToolName {
		t.Fatalf("expected the second answer taken, got %+v %v", action, errorValue)
	}
	retryRequest := provider.chatRequests[1]
	if string(retryRequest.ToolChoice) != `"required"` {
		t.Fatalf("expected a call required after an empty answer, got tool_choice %s", retryRequest.ToolChoice)
	}
	if len(retryRequest.Tools) != len(provider.chatRequests[0].Tools) {
		t.Fatalf("expected the choice among every tool kept, got %d of %d", len(retryRequest.Tools), len(provider.chatRequests[0].Tools))
	}
}

func TestAModelThatKeepsAnsweringWithNothingIsTheModelsMistakeNotAFailedTask(t *testing.T) {
	state := nativeAgentActionTestStateWithTools("write", toolcontract.BashToolName)
	provider := nativeAgentActionLanguageModel{chatResponse: silentStopResponse()}

	_, errorValue := DecideAgentAction(context.Background(), &provider, state)

	if errorValue == nil || !isUnreadableModelActionError(errorValue) {
		t.Fatalf("expected an empty answer handed back to the turn as unreadable, got %T %v", errorValue, errorValue)
	}
}

type silentThenAnsweringLanguageModel struct {
	actionCount int
}

func (languageModel *silentThenAnsweringLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *silentThenAnsweringLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == expectedChangesSchemaName {
		return model.StructuredResponse{Content: expectedChangesDocument(deleteOldTask)}, nil
	}
	return model.StructuredResponse{}, nil
}

func (languageModel *silentThenAnsweringLanguageModel) GenerateChatCompletion(_ context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	languageModel.actionCount++
	switch {
	case languageModel.actionCount == 1:
		return nativeAgentActionChatResponse("task_delete", `{"taskID":"task-1"}`), nil
	case languageModel.actionCount <= 1+maximumAgentActionCorrectionCount+1:
		return silentStopResponse(), nil
	default:
		return nativeAgentActionChatResponse("reply", `{"final":true,"message":"`+deletionReply+`","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`), nil
	}
}

func TestATurnWhoseModelFallsSilentForAStepKeepsGoing(t *testing.T) {
	languageModel := &silentThenAnsweringLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8})
	services.runner.UseDecisionModel(&scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9}})
	request := deleteRequest(taskDeleteToolSet())
	request.RequesterPersonID = "person-1"
	request.ConversationID = "conversation-1"
	request.PinnedToolNames = request.ToolSet.ListToolNames()

	result, errorValue := services.runner.RunTurn(context.Background(), request)

	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected one silent step not to fail the task, got %v %+v", errorValue, result.TaskRun)
	}
	if result.FinishMessage != deletionReply {
		t.Fatalf("expected the reply the model gave once it answered, got %q", result.FinishMessage)
	}
}

func TestAProviderThatEndsTheAnswerWithAnErrorFailedTheCallNotTheModel(t *testing.T) {
	state := nativeAgentActionTestStateWithTools("write", toolcontract.BashToolName)
	provider := nativeAgentActionLanguageModel{chatResponse: model.ChatCompletionResponse{
		FinishReason: string(model.StructuredOutputDiagnosticFinishError),
		Message:      model.ChatCompletionMessage{Role: "assistant"},
	}}

	_, errorValue := DecideAgentAction(context.Background(), &provider, state)

	if errorValue == nil || isUnreadableModelActionError(errorValue) {
		t.Fatalf("expected the provider's failure to end the call as a failure, got %T %v", errorValue, errorValue)
	}
	if len(provider.chatRequests) != 1 {
		t.Fatalf("a provider failure is not corrected by asking the model again, got %d requests", len(provider.chatRequests))
	}
}

type alwaysSilentLanguageModel struct{}

func (alwaysSilentLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (alwaysSilentLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

func (alwaysSilentLanguageModel) GenerateChatCompletion(context.Context, model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return silentStopResponse(), nil
}

func TestATurnWhoseModelNeverAnswersReadablyFailsWithinABound(t *testing.T) {
	services := newTurnRunnerTestServices(alwaysSilentLanguageModel{}, TurnOptions{MaxIterationCount: 40})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{RequesterPersonID: "person-1"},
		ConversationID:    "conversation-1",
		Prompt:            "서울 날씨 알려줘",
	})

	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusFailed {
		t.Fatalf("expected a model that never answers readably to fail the task, got %v %+v", errorValue, result.TaskRun)
	}
	unreadableCount := 0
	for _, taskEvent := range services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID) {
		if taskEvent.Name == agentcontract.TaskEventAgentUnreadableAction {
			unreadableCount++
		}
	}
	if unreadableCount != maximumConsecutiveUnreadableActionCount {
		t.Fatalf("expected the turn to stop after %d unreadable steps in a row, got %d", maximumConsecutiveUnreadableActionCount, unreadableCount)
	}
	if !strings.Contains(result.TaskRun.FailureReason, "llm action failed") {
		t.Fatalf("expected the failure to go through the model-action failure path, got %q", result.TaskRun.FailureReason)
	}
}
