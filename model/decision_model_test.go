package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestWireStateWithoutImagesIsTheState(t *testing.T) {
	state := map[string]any{"slide": 1}
	wireState, errorValue := DecisionRequest{State: state}.WireState()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !reflect.DeepEqual(wireState, state) {
		t.Fatalf("wire state = %v, want %v", wireState, state)
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
