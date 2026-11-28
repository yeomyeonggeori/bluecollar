package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func TestAnthropicAuthHeaderSelection(t *testing.T) {
	oauthRequest, _ := http.NewRequest(http.MethodPost, anthropicMessagesURL, nil)
	applyAnthropicAuthHeaders(oauthRequest, "sk-ant-oat01-extra-usage")
	if oauthRequest.Header.Get("Authorization") != "Bearer sk-ant-oat01-extra-usage" {
		t.Fatalf("expected Bearer auth header, got %q", oauthRequest.Header.Get("Authorization"))
	}
	if oauthRequest.Header.Get("anthropic-beta") != anthropicOAuthBetaHeader {
		t.Fatalf("expected oauth beta header, got %q", oauthRequest.Header.Get("anthropic-beta"))
	}
	if oauthRequest.Header.Get("x-api-key") != "" {
		t.Fatalf("did not expect x-api-key for oauth token")
	}

	apiKeyRequest, _ := http.NewRequest(http.MethodPost, anthropicMessagesURL, nil)
	applyAnthropicAuthHeaders(apiKeyRequest, "sk-ant-api03-standard")
	if apiKeyRequest.Header.Get("x-api-key") != "sk-ant-api03-standard" {
		t.Fatalf("expected x-api-key header, got %q", apiKeyRequest.Header.Get("x-api-key"))
	}
	if apiKeyRequest.Header.Get("Authorization") != "" || apiKeyRequest.Header.Get("anthropic-beta") != "" {
		t.Fatalf("did not expect oauth headers for standard api key: %+v", apiKeyRequest.Header)
	}
}

func TestAnthropicProviderChatCompletionAndMerging(t *testing.T) {
	var capturedHeaders http.Header
	var capturedBody map[string]any

	provider := &anthropicProvider{
		messagesURL:    anthropicMessagesURL,
		modelsURL:      anthropicModelsURL,
		modelName:      "claude-sonnet-5-5",
		apiKey:         "sk-ant-oat01-test",
		retryBaseDelay: 0,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				capturedHeaders = request.Header.Clone()
				raw, _ := io.ReadAll(request.Body)
				_ = json.Unmarshal(raw, &capturedBody)
				body := `{
					"stop_reason": "tool_use",
					"content": [
						{"type": "thinking", "thinking": "Check go.mod first"},
						{"type": "text", "text": "Reading go.mod."},
						{"type": "tool_use", "id": "toolu_1", "name": "file_read", "input": {"path": "go.mod"}}
					],
					"usage": {
						"input_tokens": 100,
						"cache_creation_input_tokens": 200,
						"cache_read_input_tokens": 500,
						"output_tokens": 45
					}
				}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		},
	}

	response, errorValue := provider.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
		Messages: []model.ChatCompletionMessage{
			{Role: "system", Content: "Immutable instructions"},
			{Role: "user", Content: "Inspect repo", Parts: []model.MessagePart{{Type: "image", MimeType: "image/png", DataBase64: "AQID"}}},
			{Role: "assistant", ToolCalls: []model.ChatCompletionToolCall{
				{ID: "toolu_a", Type: "function", Function: model.ChatCompletionToolCallFunction{Name: "file_read", Arguments: `{"path":"a.go"}`}},
				{ID: "toolu_b", Type: "function", Function: model.ChatCompletionToolCallFunction{Name: "file_read", Arguments: `{"path":"b.go"}`}},
			}},
			{Role: "tool", ToolCallID: "toolu_a", Content: "package a"},
			{Role: "tool", ToolCallID: "toolu_b", Content: "package b"},
			{Role: "user", Content: "<runtime_state>step 2</runtime_state>"},
		},
		Tools: []model.ChatCompletionTool{{
			Type: "function",
			Function: model.ChatCompletionFunction{
				Name:        "file_read",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
		ToolChoice:        json.RawMessage(`"required"`),
		ParallelToolCalls: true,
	})
	if errorValue != nil {
		t.Fatalf("GenerateChatCompletion failed: %v", errorValue)
	}
	if capturedHeaders.Get("Authorization") != "Bearer sk-ant-oat01-test" || capturedHeaders.Get("anthropic-version") != anthropicAPIVersion {
		t.Fatalf("unexpected headers: %+v", capturedHeaders)
	}
	messages, _ := capturedBody["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("expected 3 strictly alternating turns (user, assistant, merged user), got %d: %+v", len(messages), messages)
	}
	lastTurn, _ := messages[2].(map[string]any)
	lastContent, _ := lastTurn["content"].([]any)
	if lastTurn["role"] != "user" || len(lastContent) != 3 {
		t.Fatalf("expected merged user turn with 2 tool_results + 1 text block, got %+v", lastTurn)
	}
	toolChoice, _ := capturedBody["tool_choice"].(map[string]any)
	if toolChoice["type"] != "any" {
		t.Fatalf("expected tool_choice type any, got %+v", toolChoice)
	}
	if response.FinishReason != "tool_calls" || len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("unexpected chat response: %+v", response)
	}
	if response.Message.Reasoning != "Check go.mod first" || response.Message.ReasoningField != "thinking" {
		t.Fatalf("unexpected reasoning: %q (%q)", response.Message.Reasoning, response.Message.ReasoningField)
	}
	if response.Usage.PromptTokens != 800 || response.Usage.CachedPromptTokens != 500 || response.Usage.CompletionTokens != 45 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
}

func TestAnthropicProviderStructuredResponse(t *testing.T) {
	var capturedBody map[string]any
	provider := &anthropicProvider{
		messagesURL:    anthropicMessagesURL,
		modelName:      "claude-sonnet-5-5",
		apiKey:         "sk-ant-api03-key",
		retryBaseDelay: 0,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(request.Body)
				_ = json.Unmarshal(raw, &capturedBody)
				body := `{"stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_s","name":"verdict","input":{"passed":true}}],"usage":{"input_tokens":20,"output_tokens":8}}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		},
	}

	structured, errorValue := provider.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: "Judge outcome"},
			{Role: "user", Content: "Done"},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:     "verdict",
			Document: `{"type":"object","properties":{"passed":{"type":"boolean"}}}`,
		},
	})
	if errorValue != nil {
		t.Fatalf("GenerateStructuredResponse failed: %v", errorValue)
	}
	if structured.Content != `{"passed":true}` || structured.ProviderName != anthropicEndpointName {
		t.Fatalf("unexpected structured response: %+v", structured)
	}
	toolChoice, _ := capturedBody["tool_choice"].(map[string]any)
	if toolChoice["type"] != "tool" || toolChoice["name"] != "verdict" {
		t.Fatalf("expected forced tool_choice for schema, got %+v", toolChoice)
	}
}

func TestAnthropicProviderRetriesOnOverloaded(t *testing.T) {
	attempts := 0
	provider := &anthropicProvider{
		messagesURL:    anthropicMessagesURL,
		modelName:      "claude-sonnet-5-5",
		apiKey:         "sk-ant-api03-key",
		retryBaseDelay: 0,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return &http.Response{
						StatusCode: anthropicHTTPStatusOverload,
						Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"overloaded_error","message":"Overloaded"}}`)),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"stop_reason":"end_turn","content":[{"type":"text","text":"ready"}]}`)),
				}, nil
			}),
		},
	}

	reply, errorValue := provider.GenerateResponse(context.Background(), "ping")
	if errorValue != nil || reply != "ready" || attempts != 2 {
		t.Fatalf("reply=%q attempts=%d err=%v", reply, attempts, errorValue)
	}
}

func TestReplEndpointSwitchToClaudeAndLogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-extra")
	t.Setenv("ANTHROPIC_API_KEY", "")

	session := &conversationSession{
		options: runOptions{
			endpointURL: codexEndpointName,
			modelName:   defaultCodexModelName,
			configPath:  configFilePath(),
		},
	}
	output := &bytes.Buffer{}
	handled, shouldExit := handleReplCommand(context.Background(), session, "/login claude", output)
	if !handled || shouldExit {
		t.Fatalf("expected /login claude handled without exit")
	}
	if session.options.endpointURL != anthropicEndpointName || session.options.modelName != defaultAnthropicModelName {
		t.Fatalf("unexpected endpoint/model: %q / %q", session.options.endpointURL, session.options.modelName)
	}
	if !strings.Contains(output.String(), "Extra Usage") {
		t.Fatalf("expected Extra Usage notice, got:\n%s", output.String())
	}

	output.Reset()
	handled, shouldExit = handleReplCommand(context.Background(), session, "/models", output)
	if !handled || shouldExit {
		t.Fatalf("expected /models handled")
	}
	if !strings.Contains(output.String(), "claude-sonnet-5-5") || !strings.Contains(output.String(), "claude-opus-5-5") {
		t.Fatalf("expected claude catalog models in output, got:\n%s", output.String())
	}
}
