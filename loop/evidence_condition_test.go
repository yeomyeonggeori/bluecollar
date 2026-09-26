package loop

import (
	"encoding/json"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestEvidenceConditionUsesSemanticJSONEquality(t *testing.T) {
	condition := toolcontract.EvidenceCondition{
		ResultField: "review",
		Equals:      json.RawMessage(`{"passed":true,"scores":[1,2]}`),
	}
	result := json.RawMessage(`{"review":{"scores":[1.0,2],"passed":true}}`)

	if !resultSatisfiesEvidenceCondition(result, condition) {
		t.Fatal("expected equivalent JSON values to match regardless of field order and number representation")
	}
}
