package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var putTheTaskUnderABusiness = "그 업무 블루세이지 사업으로 넣어줘"

func labelledTaskToolSet() *toolcontract.ToolSet {
	update := declaringPathToolDefinition("task_update", "Update one task.", "task", "updated")
	update.InputSchema = json.RawMessage(`{"type":"object","properties":{"taskHint":{"type":"string","description":"The task to change."},"business":{"type":"string","description":"Business label, taken from registeredLabels.businesses."},"type":{"type":"string","description":"Company type, taken from registeredLabels.types."}}}`)
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{update})
}

func taskUpdatedWith(input string) []turnObservation {
	return []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "task_update",
		ToolInput:     json.RawMessage(input),
		Output:        toolcontract.ToolOutput{Data: json.RawMessage(`{"taskID":"task-1"}`)},
		Effects:       []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "updated", ID: "task-1"}},
	}}
}

func TestChangeCheckRefusesAValueWrittenIntoAFieldThatDoesNotHoldWhatWasAsked(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.98, "already0": 0.1}, choice: map[string]string{"field0": "business"}}
	request := AgentTurnRequest{Prompt: putTheTaskUnderABusiness, ToolSet: labelledTaskToolSet()}

	check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, []string{putTheTaskUnderABusiness}, taskUpdatedWith(`{"taskHint":"task-1","type":"business: 블루세이지"}`))

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(check.Unmet) != 1 || check.UnwrittenFields["expected0"] != "business" {
		t.Fatalf("expected the change unmet for want of the business field, got %+v", check)
	}
	if message := unmetChangesMessage(check); !strings.Contains(message, "no recorded write set that field") || !strings.Contains(message, "business") {
		t.Fatalf("expected the agent told which field no write set, got %q", message)
	}
}

func TestChangeCheckAsksTheFieldWithoutShowingWhatWasWritten(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.98}, choice: map[string]string{"field0": "business"}}
	request := AgentTurnRequest{Prompt: putTheTaskUnderABusiness, ToolSet: labelledTaskToolSet()}

	check, _ := checkExpectedChanges(context.Background(), decisionModel, request, []string{putTheTaskUnderABusiness}, taskUpdatedWith(`{"taskHint":"task-1","business":"블루세이지"}`))

	if len(check.Unmet) != 0 || len(check.UnwrittenFields) != 0 {
		t.Fatalf("expected a write that set the field to carry the change, got %+v", check)
	}
	for _, decisionRequest := range decisionModel.requests {
		if _, asksTheField := decisionRequest.Questions["field0"]; !asksTheField {
			continue
		}
		if _, showsRecords := decisionRequest.State.(map[string]any)["changedRecords"]; showsRecords {
			t.Fatal("the field question must be answered from the request alone, never from what the work wrote")
		}
		return
	}
	t.Fatal("expected a question about which field holds the change")
}

func TestChangeCheckAcceptsAFieldTheRecordAlreadyHeld(t *testing.T) {
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 0.9, "already0": 0.9}, choice: map[string]string{"field0": "business"}}
	request := AgentTurnRequest{Prompt: putTheTaskUnderABusiness, ToolSet: labelledTaskToolSet()}

	check, _ := checkExpectedChanges(context.Background(), decisionModel, request, []string{putTheTaskUnderABusiness}, taskUpdatedWith(`{"taskHint":"task-1","title":"분기 보고서 정리"}`))

	if len(check.Unmet) != 0 {
		t.Fatalf("expected a field the record already held to count, got %+v", check)
	}
}
