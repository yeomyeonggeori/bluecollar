package toolcontract

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type nullAsAbsentCases struct {
	Schemas []struct {
		Schema         any `json:"schema"`
		AbsentWhenNull any `json:"absentWhenNull"`
	} `json:"schemas"`
	Documents []struct {
		Document       any `json:"document"`
		AbsentWhenNull any `json:"absentWhenNull"`
	} `json:"documents"`
}

func TestNullIsReadAsAbsentEverywhere(t *testing.T) {
	content, errorValue := os.ReadFile("testdata/null-as-absent.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var cases nullAsAbsentCases
	if errorValue := json.Unmarshal(content, &cases); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, schemaCase := range cases.Schemas {
		if normalized := schemaWithNullAsAbsent(schemaCase.Schema); !reflect.DeepEqual(normalized, schemaCase.AbsentWhenNull) {
			t.Fatalf("schema %v\nread as %v\nwant %v", schemaCase.Schema, normalized, schemaCase.AbsentWhenNull)
		}
	}
	for _, documentCase := range cases.Documents {
		if normalized := documentWithNullAsAbsent(documentCase.Document); !reflect.DeepEqual(normalized, documentCase.AbsentWhenNull) {
			t.Fatalf("document %v\nread as %v\nwant %v", documentCase.Document, normalized, documentCase.AbsentWhenNull)
		}
	}
}

func TestAFieldThatMayBeNullMayBeLeftOut(t *testing.T) {
	contract := ToolResultContract{Schema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"},"eventID":{"anyOf":[{"type":"string"},{"type":"null"}]}},"required":["status","eventID"],"additionalProperties":false}`)}
	for _, answer := range []string{`{"status":"asked","eventID":null}`, `{"status":"asked"}`} {
		if errorValue := ValidateSuccessfulToolResult(contract, ToolResult{Output: ToolOutput{Data: json.RawMessage(answer)}}); errorValue != nil {
			t.Fatalf("%s: %v", answer, errorValue)
		}
	}
	inputSchema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"note":{"type":"string"}},"required":["title"],"additionalProperties":false}`)
	accepted, errorValue := ValidateToolInput(inputSchema, json.RawMessage(`{"title":"Weekly report","note":null}`))
	if errorValue != nil || string(accepted) != `{"title":"Weekly report"}` {
		t.Fatalf("expected a null note to be read as left out, got %s %v", accepted, errorValue)
	}
	if _, errorValue := ValidateToolInput(inputSchema, json.RawMessage(`{"title":null}`)); errorValue == nil {
		t.Fatal("expected a null required title to be read as missing")
	}
}
