package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func makeTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadBytes, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".signature"
}

func TestParseCodexAuthJSONNestedAndFlat(t *testing.T) {
	token := makeTestJWT(map[string]any{
		"exp": 1999999999,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_jwt_123",
		},
	})
	nestedDocument := []byte(`{"tokens":{"access_token":"` + token + `","refresh_token":"refresh_nested"}}`)
	nested, nestedError := parseCodexAuthJSON(nestedDocument, "/tmp/auth.json")
	if nestedError != nil {
		t.Fatalf("unexpected nested parse error: %v", nestedError)
	}
	if nested.AccessToken != token || nested.RefreshToken != "refresh_nested" {
		t.Fatalf("unexpected nested tokens: %+v", nested)
	}
	if nested.AccountID != "acct_jwt_123" || nested.ExpiresAt != 1999999999 {
		t.Fatalf("expected JWT claims extracted, got %+v", nested)
	}

	flatDocument := []byte(`{"access_token":"plain_token","refresh_token":"refresh_flat","account_id":"acct_explicit","expires_at":1888888888}`)
	flat, flatError := parseCodexAuthJSON(flatDocument, "/tmp/codex-auth.json")
	if flatError != nil {
		t.Fatalf("unexpected flat parse error: %v", flatError)
	}
	if flat.AccountID != "acct_explicit" || flat.ExpiresAt != 1888888888 {
		t.Fatalf("unexpected flat credentials: %+v", flat)
	}
}

func TestBuildCodexAuthorizeURL(t *testing.T) {
	rawURL := buildCodexAuthorizeURL("challenge_abc", "state_xyz")
	parsed, errorValue := url.Parse(rawURL)
	if errorValue != nil {
		t.Fatalf("failed to parse authorize URL: %v", errorValue)
	}
	query := parsed.Query()
	expected := map[string]string{
		"response_type":              "code",
		"client_id":                  codexOAuthClientID,
		"redirect_uri":               codexOAuthRedirectURI,
		"code_challenge":             "challenge_abc",
		"code_challenge_method":      "S256",
		"state":                      "state_xyz",
		"codex_cli_simplified_flow":  "true",
		"id_token_add_organizations": "true",
		"originator":                 codexOAuthOriginator,
	}
	for key, want := range expected {
		if got := query.Get(key); got != want {
			t.Fatalf("query[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestParseCodexSSEStreamCompletedWithToolsAndReasoning(t *testing.T) {
	stream := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"Checking files."}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Inspect workspace"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checking files."}]},{"type":"function_call","call_id":"call_1","name":"file_read","arguments":"{\"path\":\"go.mod\"}"}],"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150,"input_tokens_details":{"cached_tokens":80},"output_tokens_details":{"reasoning_tokens":10}}}}`,
		``,
		`data: [DONE]`,
	}, "\n")

	parsed, errorValue := parseCodexSSEStream(strings.NewReader(stream))
	if errorValue != nil {
		t.Fatalf("unexpected parse error: %v", errorValue)
	}
	response := parsed.toChatCompletionResponse("gpt-6.2-sol")
	if response.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want tool_calls", response.FinishReason)
	}
	if response.Message.Content != "Checking files." {
		t.Fatalf("Content = %q, want Checking files.", response.Message.Content)
	}
	if response.Message.Reasoning != "Inspect workspace" || response.Message.ReasoningField != "reasoning" {
		t.Fatalf("unexpected reasoning: %q (%q)", response.Message.Reasoning, response.Message.ReasoningField)
	}
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].ID != "call_1" || response.Message.ToolCalls[0].Function.Name != "file_read" {
		t.Fatalf("unexpected tool calls: %+v", response.Message.ToolCalls)
	}
	if response.Usage.PromptTokens != 120 || response.Usage.CachedPromptTokens != 80 || response.Usage.ReasoningTokens != 10 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}
}

func TestParseCodexSSEStreamErrors(t *testing.T) {
	failedStream := `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"rate_limit","message":"quota exceeded"}}}` + "\n"
	if _, errorValue := parseCodexSSEStream(strings.NewReader(failedStream)); errorValue == nil || !strings.Contains(errorValue.Error(), "quota exceeded") {
		t.Fatalf("expected quota exceeded error, got %v", errorValue)
	}

	errorStream := `data: {"type":"error","error":{"message":"invalid model"}}` + "\n"
	if _, errorValue := parseCodexSSEStream(strings.NewReader(errorStream)); errorValue == nil || !strings.Contains(errorValue.Error(), "invalid model") {
		t.Fatalf("expected invalid model error, got %v", errorValue)
	}
}

func TestCodexProviderGenerateChatCompletionAndStructuredResponse(t *testing.T) {
	var capturedBody map[string]any
	var capturedHeaders http.Header

	provider := &codexProvider{
		endpointURL:    codexResponsesURL,
		tokenURL:       codexOAuthTokenURL,
		modelName:      "gpt-5.4",
		retryBaseDelay: 0,
		loadAuth: func() (codexCredentials, error) {
			return codexCredentials{
				AccessToken: "access_abc",
				AccountID:   "acct_999",
				ExpiresAt:   time.Now().Add(time.Hour).Unix(),
			}, nil
		},
		httpClient: &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				capturedHeaders = request.Header.Clone()
				raw, _ := io.ReadAll(request.Body)
				capturedBody = nil
				_ = json.Unmarshal(raw, &capturedBody)
				sse := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"ok\":true}"}]}],"usage":{"input_tokens":50,"output_tokens":12}}}` + "\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader(sse)),
				}, nil
			}),
		},
	}

	chatResponse, chatError := provider.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{
		Messages: []model.ChatCompletionMessage{
			{Role: "system", Content: "System instruction"},
			{Role: "user", Content: "Read go.mod", Parts: []model.MessagePart{{Type: "image", MimeType: "image/png", DataBase64: "AQID"}}},
			{Role: "assistant", ToolCalls: []model.ChatCompletionToolCall{{ID: "call_7", Type: "function", Function: model.ChatCompletionToolCallFunction{Name: "file_read", Arguments: `{"path":"go.mod"}`}}}},
			{Role: "tool", ToolCallID: "call_7", Content: "module example"},
		},
		Tools: []model.ChatCompletionTool{{
			Type: "function",
			Function: model.ChatCompletionFunction{
				Name:        "file_read",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		}},
		ToolChoice:        json.RawMessage(`{"type":"function","function":{"name":"file_read"}}`),
		ParallelToolCalls: true,
	})
	if chatError != nil {
		t.Fatalf("GenerateChatCompletion failed: %v", chatError)
	}
	if chatResponse.ProviderName != codexEndpointName || chatResponse.Message.Content != `{"ok":true}` {
		t.Fatalf("unexpected chat response: %+v", chatResponse)
	}
	if capturedHeaders.Get("Authorization") != "Bearer access_abc" || capturedHeaders.Get("chatgpt-account-id") != "acct_999" || capturedHeaders.Get("OpenAI-Beta") != "responses=experimental" {
		t.Fatalf("unexpected headers: %+v", capturedHeaders)
	}
	if capturedBody["instructions"] != "System instruction" || capturedBody["store"] != false || capturedBody["stream"] != true {
		t.Fatalf("unexpected request envelope: %+v", capturedBody)
	}
	toolChoice, _ := capturedBody["tool_choice"].(map[string]any)
	if toolChoice["type"] != "function" || toolChoice["name"] != "file_read" {
		t.Fatalf("expected flattened tool_choice, got %+v", capturedBody["tool_choice"])
	}

	structured, structuredError := provider.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		Messages: []model.Message{
			{Role: "system", Content: "Return JSON"},
			{Role: "user", Content: "Check"},
		},
		StructuredOutputSchema: model.StructuredOutputSchema{
			Name:     "verdict",
			Document: `{"type":"object","properties":{"ok":{"type":"boolean"}}}`,
		},
	})
	if structuredError != nil {
		t.Fatalf("GenerateStructuredResponse failed: %v", structuredError)
	}
	if structured.Content != `{"ok":true}` {
		t.Fatalf("unexpected structured content: %q", structured.Content)
	}
	textBlock, _ := capturedBody["text"].(map[string]any)
	formatBlock, _ := textBlock["format"].(map[string]any)
	if formatBlock["type"] != "json_schema" || formatBlock["name"] != "verdict" {
		t.Fatalf("expected json_schema format, got %+v", capturedBody["text"])
	}
}

func TestCodexProviderRefreshesTokenOnUnauthorized(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	refreshedToken := makeTestJWT(map[string]any{
		"exp": time.Now().Add(2 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_refreshed",
		},
	})
	responsesCalls := 0
	refreshCalls := 0

	provider := &codexProvider{
		endpointURL:    codexResponsesURL,
		tokenURL:       codexOAuthTokenURL,
		modelName:      "gpt-5.4",
		retryBaseDelay: 0,
		loadAuth: func() (codexCredentials, error) {
			return codexCredentials{
				AccessToken:  "stale_token",
				RefreshToken: "refresh_1",
				AccountID:    "acct_old",
				ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			}, nil
		},
		httpClient: &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == codexOAuthTokenURL {
					refreshCalls++
					body := `{"access_token":"` + refreshedToken + `","refresh_token":"refresh_2","expires_in":3600}`
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				}
				responsesCalls++
				if responsesCalls == 1 {
					return &http.Response{
						StatusCode: http.StatusUnauthorized,
						Body:       io.NopCloser(strings.NewReader(`{"detail":"token expired"}`)),
					}, nil
				}
				if request.Header.Get("Authorization") != "Bearer "+refreshedToken {
					t.Fatalf("expected refreshed bearer token, got %q", request.Header.Get("Authorization"))
				}
				sse := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}]}}` + "\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(sse)),
				}, nil
			}),
		},
	}

	reply, errorValue := provider.GenerateResponse(context.Background(), "hello")
	if errorValue != nil {
		t.Fatalf("expected recovery after 401 refresh, got %v", errorValue)
	}
	if reply != "recovered" || refreshCalls != 1 || responsesCalls != 2 {
		t.Fatalf("reply=%q refreshCalls=%d responsesCalls=%d", reply, refreshCalls, responsesCalls)
	}
}

func TestCodexProviderRetriesOnServiceUnavailable(t *testing.T) {
	attempts := 0
	provider := &codexProvider{
		endpointURL:    codexResponsesURL,
		tokenURL:       codexOAuthTokenURL,
		modelName:      "gpt-5.4",
		retryBaseDelay: 0,
		loadAuth: func() (codexCredentials, error) {
			return codexCredentials{
				AccessToken: "valid_token",
				ExpiresAt:   time.Now().Add(time.Hour).Unix(),
			}, nil
		},
		httpClient: &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"service_unavailable","message":"try again"}}`)),
					}, nil
				}
				sse := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(sse)),
				}, nil
			}),
		},
	}

	reply, errorValue := provider.GenerateResponse(context.Background(), "ping")
	if errorValue != nil {
		t.Fatalf("expected retry to succeed, got %v", errorValue)
	}
	if reply != "ok" || attempts != 2 {
		t.Fatalf("reply=%q attempts=%d", reply, attempts)
	}
}

func TestReplEndpointSwitchToCodexAndModels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	session := &conversationSession{
		options: runOptions{
			endpointURL: geminiCLIEndpointName,
			modelName:   defaultGeminiCLIModelName,
			configPath:  configFilePath(),
		},
	}
	output := &bytes.Buffer{}
	handled, shouldExit := handleReplCommand(context.Background(), session, "/endpoint chatgpt", output)
	if !handled || shouldExit {
		t.Fatalf("expected /endpoint chatgpt to be handled without exit")
	}
	if session.options.endpointURL != codexEndpointName || session.options.modelName != defaultCodexModelName {
		t.Fatalf("unexpected endpoint/model after switch: %q / %q", session.options.endpointURL, session.options.modelName)
	}

	output.Reset()
	handled, shouldExit = handleReplCommand(context.Background(), session, "/models", output)
	if !handled || shouldExit {
		t.Fatalf("expected /models to be handled")
	}
	if !strings.Contains(output.String(), "gpt-6.2-sol") || !strings.Contains(output.String(), "gpt-6.2-astra") {
		t.Fatalf("expected codex catalog models in output, got:\n%s", output.String())
	}
}

func TestParseCodexModelsResponse(t *testing.T) {
	document := []byte(`{
		"models": [
			{"slug": "gpt-6-luna", "visibility": "list", "priority": 3},
			{"slug": "gpt-6.2-astra", "visibility": "list", "priority": 1},
			{"slug": "internal-hidden", "visibility": "hide", "priority": 0},
			{"slug": "gpt-6.2-sol", "visibility": "list", "priority": 2},
			{"slug": "gpt-6.1-terra", "visibility": "list", "priority": 2}
		]
	}`)
	models, errorValue := parseCodexModelsResponse(document)
	if errorValue != nil {
		t.Fatalf("unexpected parse error: %v", errorValue)
	}
	want := []string{"gpt-6.2-astra", "gpt-6.2-sol", "gpt-6.1-terra", "gpt-6-luna"}
	if strings.Join(models, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v, want %v", models, want)
	}
}
