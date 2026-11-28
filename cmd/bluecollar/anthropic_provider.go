package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	anthropicEndpointName       = "anthropic"
	defaultAnthropicModelName   = "claude-sonnet-5-5"
	anthropicMessagesURL        = "https://api.anthropic.com/v1/messages"
	anthropicModelsURL          = "https://api.anthropic.com/v1/models"
	anthropicAPIVersion         = "2023-06-01"
	anthropicOAuthBetaHeader    = "oauth-2025-04-20"
	anthropicDefaultMaxTokens   = 16384
	anthropicDefaultWindow      = 200000
	anthropicMaxRetries         = 3
	anthropicRetryBaseDelay     = time.Second
	anthropicHTTPStatusOverload = 529
)

var anthropicCatalogModels = []string{
	"claude-sonnet-5-5",
	"claude-opus-5-5",
	"claude-haiku-5-5",
	"claude-fable-5-2",
}

func isAnthropicEndpoint(endpointURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(endpointURL))
	return normalized == anthropicEndpointName || normalized == "claude"
}

func isAnthropicOAuthToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), "sk-ant-oat")
}

func resolveAnthropicCredential(explicitKey string) string {
	return firstNonEmpty(
		explicitKey,
		os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"),
		os.Getenv("ANTHROPIC_API_KEY"),
	)
}

func applyAnthropicAuthHeaders(request *http.Request, credential string) {
	request.Header.Set("anthropic-version", anthropicAPIVersion)
	if isAnthropicOAuthToken(credential) {
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("anthropic-beta", anthropicOAuthBetaHeader)
		return
	}
	request.Header.Set("x-api-key", credential)
}

type anthropicProvider struct {
	messagesURL    string
	modelsURL      string
	modelName      string
	apiKey         string
	httpClient     *http.Client
	retryBaseDelay time.Duration
}

func newAnthropicProvider(modelName string, apiKey string) *anthropicProvider {
	resolvedModel := strings.TrimSpace(modelName)
	if resolvedModel == "" {
		resolvedModel = defaultAnthropicModelName
	}
	return &anthropicProvider{
		messagesURL:    anthropicMessagesURL,
		modelsURL:      anthropicModelsURL,
		modelName:      resolvedModel,
		apiKey:         strings.TrimSpace(apiKey),
		httpClient:     http.DefaultClient,
		retryBaseDelay: anthropicRetryBaseDelay,
	}
}

func (provider *anthropicProvider) ContextWindowTokens(context.Context) int {
	return anthropicDefaultWindow
}

func (provider *anthropicProvider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	body := map[string]any{
		"model":      provider.modelName,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages": []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		}},
	}
	response, errorValue := provider.sendMessagesRequest(ctx, body)
	if errorValue != nil {
		return "", errorValue
	}
	return response.textContent(), nil
}

func (provider *anthropicProvider) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	systemBlocks, messages := convertModelMessagesToAnthropic(request.Messages)
	body := map[string]any{
		"model":      provider.modelName,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages":   messages,
	}
	if len(systemBlocks) > 0 {
		body["system"] = systemBlocks
	}
	schemaName := ""
	if schema := decodeJSONObject(request.StructuredOutputSchema.Document); schema != nil {
		schemaName = strings.TrimSpace(request.StructuredOutputSchema.Name)
		if schemaName == "" {
			schemaName = "structured_response"
		}
		body["tools"] = []map[string]any{{
			"name":         schemaName,
			"description":  "Return the structured response matching the required JSON schema.",
			"input_schema": schema,
		}}
		body["tool_choice"] = map[string]any{
			"type": "tool",
			"name": schemaName,
		}
	}
	response, errorValue := provider.sendMessagesRequest(ctx, body)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	content := response.textContent()
	if schemaName != "" {
		if toolInput := response.toolInputJSON(schemaName); toolInput != "" {
			content = toolInput
		}
	}
	return model.StructuredResponse{
		Content:      content,
		ProviderName: anthropicEndpointName,
		ModelName:    provider.modelName,
		Usage:        response.usage(),
	}, nil
}

func (provider *anthropicProvider) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	systemBlocks, messages := convertChatMessagesToAnthropic(request.Messages)
	body := map[string]any{
		"model":      provider.modelName,
		"max_tokens": anthropicDefaultMaxTokens,
		"messages":   messages,
	}
	if len(systemBlocks) > 0 {
		body["system"] = systemBlocks
	}
	if tools := convertChatToolsToAnthropicTools(request.Tools); len(tools) > 0 {
		body["tools"] = tools
		if choice := convertAnthropicToolChoice(request.ToolChoice, request.ParallelToolCalls); choice != nil {
			body["tool_choice"] = choice
		}
	}
	response, errorValue := provider.sendMessagesRequest(ctx, body)
	if errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	return response.toChatCompletionResponse(provider.modelName), nil
}

func (provider *anthropicProvider) GenerateRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return provider.GenerateChatCompletion(ctx, request)
}

func (provider *anthropicProvider) sendMessagesRequest(ctx context.Context, payload map[string]any) (anthropicMessageResponse, error) {
	credential := resolveAnthropicCredential(provider.apiKey)
	if credential == "" {
		return anthropicMessageResponse{}, errors.New("anthropic credential not set; run `claude setup-token` and pass it via /apikey or CLAUDE_CODE_OAUTH_TOKEN (Extra Usage), or set ANTHROPIC_API_KEY")
	}
	encodedBody, errorValue := json.Marshal(payload)
	if errorValue != nil {
		return anthropicMessageResponse{}, errorValue
	}
	for attempt := 0; ; attempt++ {
		response, statusCode, rawBody, requestError := provider.postMessage(ctx, encodedBody, credential)
		model.RecordWireExchange(ctx, model.WireExchange{
			Endpoint: provider.messagesURL,
			Request:  string(encodedBody),
			Response: rawBody,
		})
		if requestError == nil {
			return response, nil
		}
		isTransient := statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError || statusCode == anthropicHTTPStatusOverload
		if !isTransient || attempt >= anthropicMaxRetries || ctx.Err() != nil {
			return anthropicMessageResponse{}, requestError
		}
		delay := provider.retryBaseDelay << attempt
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return anthropicMessageResponse{}, requestError
		case <-timer.C:
		}
	}
}

func (provider *anthropicProvider) postMessage(ctx context.Context, encodedBody []byte, credential string) (anthropicMessageResponse, int, string, error) {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, provider.messagesURL, bytes.NewReader(encodedBody))
	if errorValue != nil {
		return anthropicMessageResponse{}, 0, "", errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	applyAnthropicAuthHeaders(httpRequest, credential)

	httpResponse, errorValue := provider.httpClient.Do(httpRequest)
	if errorValue != nil {
		return anthropicMessageResponse{}, 0, "", errorValue
	}
	defer httpResponse.Body.Close()

	bodyBytes, readError := io.ReadAll(io.LimitReader(httpResponse.Body, 10*1024*1024))
	if readError != nil {
		return anthropicMessageResponse{}, httpResponse.StatusCode, "", readError
	}
	if httpResponse.StatusCode != http.StatusOK {
		return anthropicMessageResponse{}, httpResponse.StatusCode, string(bodyBytes), fmt.Errorf("anthropic returned %d: %s", httpResponse.StatusCode, extractAnthropicHTTPError(bodyBytes))
	}
	var parsed anthropicMessageResponse
	if unmarshalError := json.Unmarshal(bodyBytes, &parsed); unmarshalError != nil {
		return anthropicMessageResponse{}, httpResponse.StatusCode, string(bodyBytes), unmarshalError
	}
	return parsed, httpResponse.StatusCode, string(bodyBytes), nil
}

func extractAnthropicHTTPError(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "empty error response"
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(trimmed, &payload) == nil && strings.TrimSpace(payload.Error.Message) != "" {
		if strings.TrimSpace(payload.Error.Type) != "" {
			return payload.Error.Type + ": " + collapsedWhitespace(payload.Error.Message)
		}
		return collapsedWhitespace(payload.Error.Message)
	}
	return collapsedWhitespace(clippedTo(string(trimmed), 240))
}

type anthropicMessageResponse struct {
	StopReason string                  `json:"stop_reason"`
	Content    []anthropicContentBlock `json:"content"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func (response anthropicMessageResponse) textContent() string {
	parts := []string{}
	for _, block := range response.Content {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (response anthropicMessageResponse) toolInputJSON(toolName string) string {
	for _, block := range response.Content {
		if block.Type == "tool_use" && block.Name == toolName && len(block.Input) > 0 {
			return string(block.Input)
		}
	}
	return ""
}

func (response anthropicMessageResponse) usage() model.Usage {
	promptTokens := response.Usage.InputTokens + response.Usage.CacheCreationInputTokens + response.Usage.CacheReadInputTokens
	return model.Usage{
		PromptTokens:       promptTokens,
		CompletionTokens:   response.Usage.OutputTokens,
		TotalTokens:        promptTokens + response.Usage.OutputTokens,
		CachedPromptTokens: response.Usage.CacheReadInputTokens,
	}
}

func (response anthropicMessageResponse) toChatCompletionResponse(modelName string) model.ChatCompletionResponse {
	textParts := []string{}
	thinkingParts := []string{}
	toolCalls := []model.ChatCompletionToolCall{}

	for _, block := range response.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				thinkingParts = append(thinkingParts, strings.TrimSpace(block.Thinking))
			}
		case "tool_use":
			arguments := strings.TrimSpace(string(block.Input))
			if arguments == "" {
				arguments = "{}"
			}
			toolCalls = append(toolCalls, model.ChatCompletionToolCall{
				ID:   block.ID,
				Type: "function",
				Function: model.ChatCompletionToolCallFunction{
					Name:      strings.TrimSpace(block.Name),
					Arguments: arguments,
				},
			})
		}
	}

	finishReason := "stop"
	if len(toolCalls) > 0 || response.StopReason == "tool_use" {
		finishReason = "tool_calls"
	} else if response.StopReason == "max_tokens" {
		finishReason = "length"
	}

	reasoning := strings.Join(thinkingParts, "\n")
	reasoningField := ""
	if reasoning != "" {
		reasoningField = "thinking"
	}

	return model.ChatCompletionResponse{
		Transport:    "http",
		ProviderName: anthropicEndpointName,
		ModelName:    modelName,
		FinishReason: finishReason,
		Message: model.ChatCompletionMessage{
			Role:           "assistant",
			Content:        strings.Join(textParts, "\n"),
			Reasoning:      reasoning,
			ReasoningField: reasoningField,
			ToolCalls:      toolCalls,
		},
		Usage: response.usage(),
	}
}

func convertModelMessagesToAnthropic(messages []model.Message) ([]map[string]any, []map[string]any) {
	chatMessages := make([]model.ChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		chatMessages = append(chatMessages, model.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
			Parts:   message.Parts,
		})
	}
	return convertChatMessagesToAnthropic(chatMessages)
}

func convertChatMessagesToAnthropic(messages []model.ChatCompletionMessage) ([]map[string]any, []map[string]any) {
	systemParts := []string{}
	type pendingTurn struct {
		role   string
		blocks []map[string]any
	}
	turns := []pendingTurn{}
	appendBlocks := func(role string, blocks []map[string]any) {
		if len(blocks) == 0 {
			return
		}
		if len(turns) > 0 && turns[len(turns)-1].role == role {
			turns[len(turns)-1].blocks = append(turns[len(turns)-1].blocks, blocks...)
			return
		}
		turns = append(turns, pendingTurn{role: role, blocks: blocks})
	}

	for _, message := range messages {
		switch message.Role {
		case "system":
			text := strings.TrimSpace(message.Content)
			if text == "" {
				continue
			}
			if len(turns) == 0 {
				systemParts = append(systemParts, text)
			} else {
				appendBlocks("user", []map[string]any{{"type": "text", "text": text}})
			}
		case "user":
			appendBlocks("user", anthropicUserBlocks(message.Content, message.Parts))
		case "assistant":
			blocks := []map[string]any{}
			if text := strings.TrimSpace(message.Content); text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			for _, toolCall := range message.ToolCalls {
				inputObject := decodeJSONObject(toolCall.Function.Arguments)
				if inputObject == nil {
					inputObject = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    toolCall.ID,
					"name":  toolCall.Function.Name,
					"input": inputObject,
				})
			}
			appendBlocks("assistant", blocks)
		case "tool":
			appendBlocks("user", []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": message.ToolCallID,
				"content":     message.Content,
			}})
		}
	}

	systemBlocks := []map[string]any{}
	if joinedSystem := strings.TrimSpace(strings.Join(systemParts, "\n\n")); joinedSystem != "" {
		systemBlocks = append(systemBlocks, map[string]any{
			"type":          "text",
			"text":          joinedSystem,
			"cache_control": map[string]any{"type": "ephemeral"},
		})
	}

	result := make([]map[string]any, 0, len(turns))
	for _, turn := range turns {
		result = append(result, map[string]any{
			"role":    turn.role,
			"content": turn.blocks,
		})
	}
	if len(result) == 0 {
		result = append(result, map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": "Continue."}},
		})
	}
	return systemBlocks, result
}

func anthropicUserBlocks(content string, parts []model.MessagePart) []map[string]any {
	blocks := []map[string]any{}
	if text := strings.TrimSpace(content); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part.Type), "image") && strings.TrimSpace(part.DataBase64) != "" && strings.TrimSpace(part.MimeType) != "" {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": strings.TrimSpace(part.MimeType),
					"data":       strings.TrimSpace(part.DataBase64),
				},
			})
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
	}
	return blocks
}

func convertChatToolsToAnthropicTools(tools []model.ChatCompletionTool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		inputSchema := decodeJSONObject(string(tool.Function.Parameters))
		if inputSchema == nil {
			inputSchema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		entry := map[string]any{
			"name":         tool.Function.Name,
			"input_schema": inputSchema,
		}
		if description := strings.TrimSpace(tool.Function.Description); description != "" {
			entry["description"] = description
		}
		result = append(result, entry)
	}
	if len(result) > 0 {
		result[len(result)-1]["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	return result
}

func convertAnthropicToolChoice(rawChoice json.RawMessage, parallelToolCalls bool) map[string]any {
	trimmed := strings.TrimSpace(string(rawChoice))
	if trimmed == "" {
		return nil
	}
	var decoded any
	if json.Unmarshal([]byte(trimmed), &decoded) != nil {
		return nil
	}
	disableParallel := !parallelToolCalls
	switch typed := decoded.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "auto":
			return map[string]any{"type": "auto", "disable_parallel_tool_use": disableParallel}
		case "required", "any":
			return map[string]any{"type": "any", "disable_parallel_tool_use": disableParallel}
		case "none":
			return map[string]any{"type": "none"}
		}
	case map[string]any:
		if choiceType, _ := typed["type"].(string); choiceType == "function" {
			if functionMap, hasFunction := typed["function"].(map[string]any); hasFunction {
				if name, _ := functionMap["name"].(string); strings.TrimSpace(name) != "" {
					return map[string]any{
						"type":                      "tool",
						"name":                      strings.TrimSpace(name),
						"disable_parallel_tool_use": disableParallel,
					}
				}
			}
		}
	}
	return nil
}

func discoverAnthropicModels(ctx context.Context, explicitKey string) ([]string, error) {
	credential := resolveAnthropicCredential(explicitKey)
	if credential == "" {
		return append([]string{}, anthropicCatalogModels...), nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	models, fetchError := fetchAnthropicModels(ctx, client, anthropicModelsURL, credential)
	if fetchError != nil || len(models) == 0 {
		return append([]string{}, anthropicCatalogModels...), nil
	}
	return models, nil
}

func fetchAnthropicModels(ctx context.Context, client *http.Client, targetURL string, credential string) ([]string, error) {
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if errorValue != nil {
		return nil, errorValue
	}
	applyAnthropicAuthHeaders(request, credential)
	response, errorValue := client.Do(request)
	if errorValue != nil {
		return nil, errorValue
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic models returned %d", response.StatusCode)
	}
	body, errorValue := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if errorValue != nil {
		return nil, errorValue
	}
	return parseModelListResponse(body)
}
