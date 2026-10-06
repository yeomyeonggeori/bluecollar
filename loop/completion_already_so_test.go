package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	tripListing      = `{"events":[{"id":"event-1","title":"미국 출장","startsAt":"2026-10-05","endsAt":"2026-10-17"}]}`
	alreadySoReply   = "미국 출장 일정은 이미 10월 17일까지로 되어 있습니다."
	tripEndCorrected = "한국 돌아오면 결국 17일이긴 하더라"
)

var extendTrip = expectedChange{Change: "calendar updated", Asked: tripEndCorrected}

func calendarToolSet() *toolcontract.ToolSet {
	listing := testToolDescriptor("event_list")
	listing.Namespace = "calendar"
	listing.OutputSchema = json.RawMessage(`{"type":"object","properties":{"events":{"type":"array"}},"additionalProperties":false}`)
	listing.ResultContract = &toolcontract.ToolResultContract{Schema: listing.OutputSchema}
	update := declaringToolDefinition("event_update", "calendar", "updated")
	update.Namespace = "calendar"
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{listing, update})
	registerTestTool(toolSet, listing, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolSuccessData("1 event", json.RawMessage(tripListing)), nil
	})
	return toolSet
}

func tripListingObservation() turnObservation {
	observation := successfulSideEffectObservation("obs-001", "event_list", `{}`, "1 event")
	observation.Output.Data = json.RawMessage(tripListing)
	return observation
}

func tripRequest() AgentTurnRequest {
	return AgentTurnRequest{
		Prompt:  "다시. 근데 미국 시간으로 15일 비행기라 " + tripEndCorrected + ".",
		ToolSet: calendarToolSet(),
	}
}

func TestAChangeNothingRecordedIsJudgedAgainstTheLookupsOfItsRecords(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9}}

	check, errorValue := checkExpectedChanges(context.Background(), decisionModel, tripRequest(), []expectedChange{extendTrip}, []turnObservation{tripListingObservation()})

	if errorValue != nil || len(check.Unmet) != 0 {
		t.Fatalf("expected a change the lookups show already so to be met, got %+v error=%v", check, errorValue)
	}
	if len(decisionModel.requests) != 1 {
		t.Fatalf("expected the lookups judged once, got %d decision calls", len(decisionModel.requests))
	}
	state, _ := json.Marshal(decisionModel.requests[0].State)
	if !strings.Contains(string(state), `"lookups":[{"tool":"event_list"`) {
		t.Fatalf("expected the calendar lookup in the judged state, got %s", state)
	}
}

func TestAChangeNothingRecordedStaysUnmetWhenTheLookupsDoNotShowIt(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.1}}

	check, _ := checkExpectedChanges(context.Background(), decisionModel, tripRequest(), []expectedChange{extendTrip}, []turnObservation{tripListingObservation()})

	if len(check.Unmet) != 1 || len(check.Unrecorded) != 1 {
		t.Fatalf("expected the change unmet and named as unrecorded, got %+v", check)
	}
}

type alreadySoLanguageModel struct {
	actionCount int
}

func (languageModel *alreadySoLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (languageModel *alreadySoLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == expectedChangesSchemaName {
		return model.StructuredResponse{Content: expectedChangesDocument(extendTrip)}, nil
	}
	return model.StructuredResponse{}, nil
}

func (languageModel *alreadySoLanguageModel) GenerateChatCompletion(context.Context, model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	languageModel.actionCount++
	toolCall := nativeAgentActionToolCall("event_list", `{}`)
	if languageModel.actionCount > 1 {
		toolCall = nativeAgentActionToolCall("reply", `{"final":true,"message":"`+alreadySoReply+`","goalStatus":"satisfied","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`)
	}
	return model.ChatCompletionResponse{FinishReason: "tool_calls", Message: model.ChatCompletionMessage{Role: "assistant", ToolCalls: []model.ChatCompletionToolCall{toolCall}}}, nil
}

func TestARequestWhoseChangeIsAlreadySoCompletesOnceALookupShowsIt(t *testing.T) {
	languageModel := &alreadySoLanguageModel{}
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxIterationCount: 8})
	services.runner.UseDecisionModel(decisionModel)
	request := tripRequest()
	request.RequesterPersonID = "person-1"
	request.ConversationID = "conversation-1"
	request.PinnedToolNames = request.ToolSet.ListToolNames()

	result, errorValue := services.runner.RunTurn(context.Background(), request)

	if errorValue != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected a change already in place to complete the task, got %v %+v", errorValue, result.TaskRun)
	}
	if result.FinishMessage != alreadySoReply {
		t.Fatalf("expected the model's own reply delivered unchanged, got %q", result.FinishMessage)
	}
	if languageModel.actionCount != 2 {
		t.Fatalf("expected one lookup and one finish, got %d actions", languageModel.actionCount)
	}
	if taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), agentcontract.TaskEventAgentCompletionRequired, "") {
		t.Fatal("expected no completion refusal for a change the lookup shows already so")
	}
}
