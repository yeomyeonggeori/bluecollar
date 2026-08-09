package openaicompatible

import (
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func TestToolCallsAreReadFromTheEndpointsOwnFieldName(t *testing.T) {
	responseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"terminal_run","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)

	response, errorValue := decodeChatCompletion(responseBody, "any/model")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatal("an endpoint sends tool_calls while the internal type is tagged toolCalls, and decoding one straight into the other silently drops every call the model made")
	}
	call := response.Message.ToolCalls[0]
	if call.ID != "call-1" || call.Function.Name != "terminal_run" || call.Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("expected the call to survive decoding intact, got %+v", call)
	}
}

func TestMessagePartsReachTheEndpointAsContent(t *testing.T) {
	messages := []model.ChatCompletionMessage{{
		Role:    "user",
		Content: "do this: ",
		Parts:   []model.MessagePart{{Text: "list the workspace"}},
	}}

	chat := chatCompletionMessages(messages)

	if chat[0]["content"] != "do this: list the workspace" {
		t.Fatalf("a prompt whose text lives in parts would reach the model empty, got %q", chat[0]["content"])
	}
}

func TestTheCostTheEndpointChargedIsRecorded(t *testing.T) {
	responseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"terminal_run","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"cost":0.0034}}`)

	response, errorValue := decodeChatCompletion(responseBody, "any/model")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Usage.CostUSD != 0.0034 {
		t.Fatalf("a ceiling meant to bound spend cannot be written in money while every run reports zero cost, got %v", response.Usage.CostUSD)
	}
}

func TestThePromptTokensTheEndpointServedFromCacheAreRecorded(t *testing.T) {
	responseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"terminal_run","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10000,"completion_tokens":2,"total_tokens":10002,"prompt_tokens_details":{"cached_tokens":9000}}}`)

	response, errorValue := decodeChatCompletion(responseBody, "any/model")

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if response.Usage.CachedPromptTokens != 9000 {
		t.Fatalf("a run that reports no cached tokens reads as a run that caches nothing, got %v", response.Usage.CachedPromptTokens)
	}
}
