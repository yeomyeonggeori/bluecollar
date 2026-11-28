package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func TestGeminiCLIProviderChatCompletionRoundTrip(t *testing.T) {
	var capturedArgs []string
	var capturedBody map[string]any

	provider := newGeminiCLIProvider("gemini-4-hydrogen", "")
	provider.runner = func(_ context.Context, _ string, args []string, stdin []byte) ([]byte, []byte, error) {
		capturedArgs = append([]string{}, args...)
		if errorValue := json.Unmarshal(stdin, &capturedBody); errorValue != nil {
			t.Fatalf("invalid request JSON sent to gemini-api: %v", errorValue)
		}
		response := `{
			"id": "int-1",
			"status": "requires_action",
			"outputs": [
				{"type": "thought", "signature": "sig-xyz", "summary": [{"type": "text", "text": "inspect files first"}]},
				{"type": "function_call", "id": "call-1", "name": "bash", "arguments": {"command": "ls -la"}}
			],
			"usage": {
				"total_input_tokens": 1200,
				"total_output_tokens": 85,
				"total_cached_tokens": 400,
				"total_thought_tokens": 40
			}
		}`
		return []byte(response), nil, nil
	}

	response, errorValue := provider.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
		Messages: []model.ChatCompletionMessage{
			{Role: "system", Content: "Be careful."},
			{Role: "user", Content: "List the files."},
			{
				Role:           "assistant",
				Reasoning:      "sig-prev",
				ReasoningField: geminiThoughtSignatureSlot,
				ToolCalls: []model.ChatCompletionToolCall{{
					ID:       "obs-001",
					Type:     "function",
					Function: model.ChatCompletionToolCallFunction{Name: "bash", Arguments: `{"command":"pwd"}`},
				}},
			},
			{Role: "tool", ToolCallID: "obs-001", Content: "/tmp/work"},
		},
		Tools: []model.ChatCompletionTool{{
			Type: "function",
			Function: model.ChatCompletionFunction{
				Name:        "bash",
				Description: "Run a shell command",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
			},
		}},
		ToolChoice: json.RawMessage(`"required"`),
	})
	if errorValue != nil {
		t.Fatalf("GenerateChatCompletion failed: %v", errorValue)
	}

	joinedArgs := strings.Join(capturedArgs, " ")
	if !strings.Contains(joinedArgs, "--no-interactive --output-format json --body @-") || strings.Contains(joinedArgs, "--stream") {
		t.Fatalf("unexpected CLI args (must not duplicate --stream alongside --body): %+v", capturedArgs)
	}
	if capturedBody["model"] != "gemini-4-hydrogen" || capturedBody["system_instruction"] != "Be careful." {
		t.Fatalf("unexpected request metadata: %+v", capturedBody)
	}
	generationConfig, _ := capturedBody["generation_config"].(map[string]any)
	if generationConfig["tool_choice"] != "any" {
		t.Fatalf("expected required tool_choice to map to 'any', got %+v", generationConfig)
	}
	steps, _ := capturedBody["input"].([]any)
	if len(steps) != 4 {
		t.Fatalf("expected 4 interaction steps (user, thought, function_call, function_result), got %d: %+v", len(steps), steps)
	}
	if response.FinishReason != "tool_calls" || len(response.Message.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", response)
	}
	if response.Message.ToolCalls[0].Function.Name != "bash" || response.Message.ToolCalls[0].Function.Arguments != `{"command": "ls -la"}` {
		t.Fatalf("unexpected decoded tool call: %+v", response.Message.ToolCalls[0])
	}
	if response.Message.Reasoning != "sig-xyz" || response.Message.ReasoningField != geminiThoughtSignatureSlot {
		t.Fatalf("expected thought signature preserved, got %q (%q)", response.Message.Reasoning, response.Message.ReasoningField)
	}
	if response.Usage.PromptTokens != 1200 || response.Usage.CompletionTokens != 85 || response.Usage.CachedPromptTokens != 400 || response.Usage.ReasoningTokens != 40 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
}

func TestGeminiCLIProviderStructuredResponse(t *testing.T) {
	var capturedBody map[string]any
	provider := newGeminiCLIProvider("gemini-4-argon", "")
	provider.runner = func(_ context.Context, _ string, _ []string, stdin []byte) ([]byte, []byte, error) {
		_ = json.Unmarshal(stdin, &capturedBody)
		return []byte(`{"status":"completed","outputs":[{"type":"model_output","content":[{"type":"text","text":"{\"ok\":true}"}]}]}`), nil, nil
	}

	result, errorValue := provider.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: "Return JSON."},
			{Role: "user", Content: "Check."},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:     "test_schema",
			Document: `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}`,
		},
	})
	if errorValue != nil {
		t.Fatalf("GenerateStructuredResponse failed: %v", errorValue)
	}
	if result.Content != `{"ok":true}` {
		t.Fatalf("unexpected structured content: %q", result.Content)
	}
	responseFormat, _ := capturedBody["response_format"].(map[string]any)
	schema, _ := responseFormat["schema"].(map[string]any)
	if responseFormat["mime_type"] != "application/json" || schema["$schema"] != nil {
		t.Fatalf("expected sanitized JSON response_format, got %+v", responseFormat)
	}
}

func TestResolveConnectionDefaultsAutoSelectsGeminiCLI(t *testing.T) {
	emptyEnv := func(string) string { return "" }
	endpoint, apiKey, modelName := resolveConnectionDefaultsWith("", "", "", savedConfig{}, emptyEnv, true, true)
	if endpoint != geminiCLIEndpointName || apiKey != "" || modelName != defaultGeminiCLIModelName {
		t.Fatalf("expected gemini-api auto-selection, got %q %q %q", endpoint, apiKey, modelName)
	}

	session := &conversationSession{options: runOptions{endpointURL: defaultLocalEndpointURL}}
	output := &bytes.Buffer{}
	handleReplCommand(context.Background(), session, "/endpoint gemini-api", output)
	if session.options.endpointURL != geminiCLIEndpointName || session.options.modelName != defaultGeminiCLIModelName {
		t.Fatalf("expected /endpoint gemini-api to select default model, got %q %q", session.options.endpointURL, session.options.modelName)
	}
	if _, isGemini := session.languageModel.(*geminiCLIProvider); !isGemini {
		t.Fatalf("expected geminiCLIProvider after /endpoint gemini-api, got %T", session.languageModel)
	}
}

func TestParseGeminiCLIModelLists(t *testing.T) {
	fromJSON := parseGeminiCLIModelsJSON([]byte(`{"models":[{"name":"models/gemini-4-hydrogen"},{"name":"models/gemini-4-argon"}]}`))
	if len(fromJSON) != 2 || fromJSON[0] != "gemini-4-hydrogen" || fromJSON[1] != "gemini-4-argon" {
		t.Fatalf("unexpected JSON model list: %+v", fromJSON)
	}
	fromText := parseGeminiCLIModelsText("Text:\n  gemini-4-hydrogen (default)  Fast model\n  gemini-4-carbon  Reasoning model\n")
	if len(fromText) != 2 || fromText[0] != "gemini-4-hydrogen" || fromText[1] != "gemini-4-carbon" {
		t.Fatalf("unexpected text model list: %+v", fromText)
	}
}

func TestGeminiCLIProviderRetriesTransient503AndFormatsNestedError(t *testing.T) {
	attempts := 0
	provider := newGeminiCLIProvider("gemini-4-hydrogen", "")
	provider.retryBaseDelay = 0
	provider.runner = func(_ context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
		attempts++
		if attempts < 2 {
			return nil, []byte(`{"error":{"code":"service_unavailable","message":"high demand"},"error_type":"server_error","status_code":503}`), context.DeadlineExceeded
		}
		return []byte(`{"status":"completed","outputs":[{"type":"text","text":"hello"}]}`), nil, nil
	}

	reply, errorValue := provider.GenerateResponse(context.Background(), "hi")
	if errorValue != nil || reply != "hello" || attempts != 2 {
		t.Fatalf("expected transient 503 retry to succeed on attempt 2, got reply=%q err=%v attempts=%d", reply, errorValue, attempts)
	}

	detail, isTransient := parseGeminiCLIError([]byte(`{"error":{"code":"service_unavailable","message":"high demand"},"error_type":"server_error","status_code":503}`), nil, context.DeadlineExceeded)
	if !isTransient || detail != "503 service_unavailable: high demand" {
		t.Fatalf("unexpected parsed nested error: detail=%q transient=%t", detail, isTransient)
	}
}
