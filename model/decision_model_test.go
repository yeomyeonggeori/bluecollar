package model

import (
	"encoding/json"
	"testing"
)

func TestWireStateWithoutImagesIsTheStateAsJSON(t *testing.T) {
	wireState, errorValue := DecisionRequest{State: map[string]any{"slide": 1}}.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if got := marshalForTest(t, wireState); got != `{"slide":1}` {
		t.Fatalf("wire state = %s, want {\"slide\":1}", got)
	}
}

type orderedStep struct {
	Record string          `json:"record"`
	Change string          `json:"change"`
	Holds  json.RawMessage `json:"holds"`
}

func TestWireStateSortsEveryKeyHowEverTheStateWasWritten(t *testing.T) {
	state := map[string]any{
		"request": "견적서를 pdf로",
		"step":    orderedStep{Record: "file 1", Change: "file attached", Holds: json.RawMessage(`{"schema":"kr/quote","given":{"recipient":"A","contact":"B","items":[{"unitPrice":185000,"name":"C"}]},"blanks":[]}`)},
	}
	want := `{"request":"견적서를 pdf로","step":{"change":"file attached","holds":{"blanks":[],"given":{"contact":"B","items":[{"name":"C","unitPrice":185000}],"recipient":"A"},"schema":"kr/quote"},"record":"file 1"}}`
	wireState, errorValue := DecisionRequest{State: state}.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if got := marshalForTest(t, wireState); got != want {
		t.Fatalf("wire state = %s, want %s", got, want)
	}
	withImage, errorValue := DecisionRequest{State: state, Images: []DecisionImage{{MediaType: "image/png", Data: []byte("abc")}}}.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	parts, isParts := withImage.([]decisionContentPart)
	if !isParts || parts[0].Text != want {
		t.Fatalf("state text = %v, want %s", withImage, want)
	}
}

func TestWireStateKeepsLargeNumbersExact(t *testing.T) {
	wireState, errorValue := DecisionRequest{State: map[string]any{"amount": json.RawMessage(`12345678901234567890.50`)}}.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if got := marshalForTest(t, wireState); got != `{"amount":12345678901234567890.50}` {
		t.Fatalf("wire state = %s", got)
	}
}

func TestWireStateKeepsStringStateVerbatim(t *testing.T) {
	request := DecisionRequest{State: "the state", Images: []DecisionImage{{MediaType: "image/png", Data: []byte("abc")}}}
	wireState, errorValue := request.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	want := `[{"type":"text","text":"the state"},{"type":"image_url","image_url":{"url":"data:image/png;base64,YWJj"}}]`
	if got := marshalForTest(t, wireState); got != want {
		t.Fatalf("wire state = %s, want %s", got, want)
	}
}

func TestWireStateEncodesStructuredStateAsText(t *testing.T) {
	request := DecisionRequest{State: map[string]any{"slide": 1}, Images: []DecisionImage{{MediaType: "image/jpeg", Data: []byte("z")}}}
	wireState, errorValue := request.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	want := `[{"type":"text","text":"{\"slide\":1}"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,eg=="}}]`
	if got := marshalForTest(t, wireState); got != want {
		t.Fatalf("wire state = %s, want %s", got, want)
	}
}

func marshalForTest(t *testing.T, value any) string {
	t.Helper()
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	return string(encoded)
}
