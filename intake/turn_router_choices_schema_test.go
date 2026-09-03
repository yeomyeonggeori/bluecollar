package intake

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func choicesFieldOf(t *testing.T, request agentcontract.AgentRequest) (map[string]any, bool) {
	t.Helper()
	var document struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if errorValue := json.Unmarshal([]byte(turnRouterSchema(request)), &document); errorValue != nil {
		t.Fatal(errorValue)
	}
	raw, isPresent := document.Properties["choices"]
	if !isPresent {
		return nil, false
	}
	var choices map[string]any
	if errorValue := json.Unmarshal(raw, &choices); errorValue != nil {
		t.Fatal(errorValue)
	}
	return choices, true
}

// A choice the model may reply with is an enum of the keys on offer. Nothing on
// offer means an empty enum, and a strict schema carrying one is refused by the
// provider before the model ever reads it: the answer comes back empty, the
// turn falls through to the local model, and the person gets a reply about
// nothing they asked. The field belongs in the schema only when there is
// something to choose.
func TestAChoiceNobodyOffersIsNotInTheSchema(t *testing.T) {
	request := agentcontract.AgentRequest{
		PendingChoice: agentcontract.PendingChoiceContext{TaskRunID: "a-task", Question: "which one?"},
	}

	if choices, isPresent := choicesFieldOf(t, request); isPresent {
		t.Errorf("a pending choice with no options put choices in the schema: %v", choices)
	}
}

func TestAChoiceOnOfferIsAnEnumOfWhatIsOffered(t *testing.T) {
	request := agentcontract.AgentRequest{
		PendingChoice: agentcontract.PendingChoiceContext{
			TaskRunID: "a-task",
			Question:  "which one?",
			Options: []agentcontract.ChoiceReplyOption{
				{Key: "retry"},
				{Key: "add"},
			},
		},
	}

	choices, isPresent := choicesFieldOf(t, request)
	if !isPresent {
		t.Fatal("a pending choice with options left choices out of the schema")
	}
	items, _ := choices["items"].(map[string]any)
	enumerated, _ := items["enum"].([]any)
	if len(enumerated) == 0 {
		t.Fatalf("the choices enum is empty: %v", choices)
	}
}
