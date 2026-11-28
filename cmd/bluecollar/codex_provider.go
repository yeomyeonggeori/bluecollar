package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	codexResponsesURL         = "https://chatgpt.com/backend-api/codex/responses"
	codexModelsURL            = "https://chatgpt.com/backend-api/codex/models"
	codexDefaultContextWindow = 272000
	codexMaxRetries           = 3
	codexRetryBaseDelay       = time.Second
	codexMaxSSEScanBytes      = 10 * 1024 * 1024
)

var codexCatalogModels = []string{
	"gpt-6.2-sol",
	"gpt-6.2-astra",
	"gpt-6.1-terra",
	"gpt-6-luna",
	"gpt-6.1-sol",
}

type codexProvider struct {
	endpointURL    string
	tokenURL       string
	modelName      string
	httpClient     *http.Client
	retryBaseDelay time.Duration
	loadAuth       func() (codexCredentials, error)
	mutex          sync.Mutex
	cachedAuth     codexCredentials
	hasCachedAuth  bool
}

func newCodexProvider(modelName string) *codexProvider {
	resolvedModel := strings.TrimSpace(modelName)
	if resolvedModel == "" {
		resolvedModel = defaultCodexModelName
	}
	return &codexProvider{
		endpointURL:    codexResponsesURL,
		tokenURL:       codexOAuthTokenURL,
		modelName:      resolvedModel,
		httpClient:     http.DefaultClient,
		retryBaseDelay: codexRetryBaseDelay,
		loadAuth:       loadCodexCredentials,
	}
}

func (provider *codexProvider) ContextWindowTokens(context.Context) int {
	return codexDefaultContextWindow
}

func (provider *codexProvider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	body := map[string]any{
		"model":        provider.modelName,
		"instructions": "You are a helpful assistant.",
		"store":        false,
		"stream":       true,
		"input": []map[string]any{{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": prompt,
			}},
		}},
	}
	completed, errorValue := provider.sendResponsesRequest(ctx, body)
	if errorValue != nil {
		return "", errorValue
	}
	return completed.textContent(), nil
}

func (provider *codexProvider) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	instructions, inputItems := convertModelMessagesToCodexInput(request.Messages)
	body := map[string]any{
		"model":        provider.modelName,
		"instructions": instructions,
		"store":        false,
		"stream":       true,
		"input":        inputItems,
	}
	if schema := decodeJSONObject(request.StructuredOutputSchema.Document); schema != nil {
		schemaName := strings.TrimSpace(request.StructuredOutputSchema.Name)
		if schemaName == "" {
			schemaName = "structured_response"
		}
		body["text"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   schemaName,
				"schema": schema,
				"strict": false,
			},
		}
	}
	completed, errorValue := provider.sendResponsesRequest(ctx, body)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	return model.StructuredResponse{
		Content:      completed.textContent(),
		ProviderName: codexEndpointName,
		ModelName:    provider.modelName,
		Usage:        completed.usage(),
	}, nil
}

func (provider *codexProvider) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	instructions, inputItems := convertChatMessagesToCodexInput(request.Messages)
	body := map[string]any{
		"model":        provider.modelName,
		"instructions": instructions,
		"store":        false,
		"stream":       true,
		"input":        inputItems,
	}
	if tools := convertChatToolsToCodexTools(request.Tools); len(tools) > 0 {
		body["tools"] = tools
		body["parallel_tool_calls"] = request.ParallelToolCalls
	}
	if toolChoice := convertCodexToolChoice(request.ToolChoice); toolChoice != nil {
		body["tool_choice"] = toolChoice
	}
	completed, errorValue := provider.sendResponsesRequest(ctx, body)
	if errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	return completed.toChatCompletionResponse(provider.modelName), nil
}

func (provider *codexProvider) GenerateRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return provider.GenerateChatCompletion(ctx, request)
}

func (provider *codexProvider) currentCredentials(ctx context.Context, forceRefresh bool) (codexCredentials, error) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()

	if !provider.hasCachedAuth {
		loaded, errorValue := provider.loadAuth()
		if errorValue != nil {
			return codexCredentials{}, errorValue
		}
		provider.cachedAuth = loaded
		provider.hasCachedAuth = true
	}
	if forceRefresh || provider.cachedAuth.isExpiringSoon(time.Now()) {
		if strings.TrimSpace(provider.cachedAuth.RefreshToken) != "" {
			refreshed, errorValue := refreshCodexCredentials(ctx, provider.httpClient, provider.tokenURL, provider.cachedAuth)
			if errorValue == nil {
				provider.cachedAuth = refreshed
				return provider.cachedAuth, nil
			}
			if forceRefresh {
				return codexCredentials{}, errorValue
			}
		}
	}
	return provider.cachedAuth, nil
}

func (provider *codexProvider) sendResponsesRequest(ctx context.Context, payload map[string]any) (codexCompletedResponse, error) {
	encodedBody, errorValue := json.Marshal(payload)
	if errorValue != nil {
		return codexCompletedResponse{}, errorValue
	}
	didRefreshAuth := false
	for attempt := 0; ; attempt++ {
		credentials, authError := provider.currentCredentials(ctx, false)
		if authError != nil {
			return codexCompletedResponse{}, authError
		}
		response, statusCode, rawBody, requestError := provider.postStream(ctx, encodedBody, credentials)
		model.RecordWireExchange(ctx, model.WireExchange{
			Endpoint: provider.endpointURL,
			Request:  string(encodedBody),
			Response: rawBody,
		})
		if requestError == nil {
			return response, nil
		}
		if statusCode == http.StatusUnauthorized && !didRefreshAuth {
			didRefreshAuth = true
			if _, refreshError := provider.currentCredentials(ctx, true); refreshError == nil {
				continue
			}
		}
		isTransient := statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
		if !isTransient || attempt >= codexMaxRetries || ctx.Err() != nil {
			return codexCompletedResponse{}, requestError
		}
		delay := provider.retryBaseDelay << attempt
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return codexCompletedResponse{}, requestError
		case <-timer.C:
		}
	}
}

func (provider *codexProvider) postStream(ctx context.Context, encodedBody []byte, credentials codexCredentials) (codexCompletedResponse, int, string, error) {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpointURL, bytes.NewReader(encodedBody))
	if errorValue != nil {
		return codexCompletedResponse{}, 0, "", errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	httpRequest.Header.Set("OpenAI-Beta", "responses=experimental")
	httpRequest.Header.Set("originator", codexOAuthOriginator)
	if credentials.AccountID != "" {
		httpRequest.Header.Set("chatgpt-account-id", credentials.AccountID)
	}

	httpResponse, errorValue := provider.httpClient.Do(httpRequest)
	if errorValue != nil {
		return codexCompletedResponse{}, 0, "", errorValue
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(httpResponse.Body, 512*1024))
		return codexCompletedResponse{}, httpResponse.StatusCode, string(bodyBytes), fmt.Errorf("codex returned %d: %s", httpResponse.StatusCode, extractCodexHTTPError(bodyBytes))
	}
	bodyBytes, readError := io.ReadAll(httpResponse.Body)
	if readError != nil {
		return codexCompletedResponse{}, httpResponse.StatusCode, "", readError
	}
	parsed, parseError := parseCodexSSEStream(bytes.NewReader(bodyBytes))
	return parsed, httpResponse.StatusCode, string(bodyBytes), parseError
}

func extractCodexHTTPError(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "empty error response"
	}
	var payload struct {
		Detail string `json:"detail"`
		Error  struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(trimmed, &payload) == nil {
		if text := firstNonEmpty(payload.Error.Message, payload.Detail); text != "" {
			if payload.Error.Code != "" {
				return payload.Error.Code + ": " + collapsedWhitespace(text)
			}
			return collapsedWhitespace(text)
		}
	}
	return collapsedWhitespace(clippedTo(string(trimmed), 240))
}

type codexErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type codexCompletedResponse struct {
	Status    string            `json:"status"`
	Output    []codexOutputItem `json:"output"`
	Usage     *codexUsage       `json:"usage"`
	Error     *codexErrorBody   `json:"error"`
	DeltaText string            `json:"-"`
}

type codexOutputItem struct {
	Type      string               `json:"type"`
	Role      string               `json:"role"`
	CallID    string               `json:"call_id"`
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Arguments string               `json:"arguments"`
	Content   []codexOutputContent `json:"content"`
	Summary   []codexOutputContent `json:"summary"`
}

type codexOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func parseCodexSSEStream(reader io.Reader) (codexCompletedResponse, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), codexMaxSSEScanBytes)

	var completed codexCompletedResponse
	hasCompleted := false
	var deltaBuilder strings.Builder
	doneItems := []codexOutputItem{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event struct {
			Type     string                  `json:"type"`
			Delta    string                  `json:"delta"`
			Item     *codexOutputItem        `json:"item"`
			Response *codexCompletedResponse `json:"response"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		switch event.Type {
		case "response.output_text.delta":
			deltaBuilder.WriteString(event.Delta)
		case "response.output_item.done":
			if event.Item != nil {
				doneItems = append(doneItems, *event.Item)
			}
		case "response.completed", "response.done":
			if event.Response != nil {
				completed = *event.Response
				hasCompleted = true
			}
		case "response.failed":
			if event.Response != nil && event.Response.Error != nil && strings.TrimSpace(event.Response.Error.Message) != "" {
				return codexCompletedResponse{}, errors.New("codex response failed: " + strings.TrimSpace(event.Response.Error.Message))
			}
			return codexCompletedResponse{}, errors.New("codex response failed: " + collapsedWhitespace(payload))
		case "error":
			if event.Error != nil && event.Error.Message != "" {
				return codexCompletedResponse{}, errors.New("codex error: " + event.Error.Message)
			}
			return codexCompletedResponse{}, errors.New("codex error: " + collapsedWhitespace(payload))
		}
	}
	if scanError := scanner.Err(); scanError != nil {
		return codexCompletedResponse{}, scanError
	}
	if !hasCompleted {
		completed.Output = doneItems
	}
	completed.DeltaText = deltaBuilder.String()
	return completed, nil
}

func (response codexCompletedResponse) textContent() string {
	parts := []string{}
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if (content.Type == "output_text" || content.Type == "text") && content.Text != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return response.DeltaText
}

func (response codexCompletedResponse) usage() model.Usage {
	if response.Usage == nil {
		return model.Usage{}
	}
	total := response.Usage.TotalTokens
	if total == 0 {
		total = response.Usage.InputTokens + response.Usage.OutputTokens
	}
	return model.Usage{
		PromptTokens:       response.Usage.InputTokens,
		CompletionTokens:   response.Usage.OutputTokens,
		TotalTokens:        total,
		CachedPromptTokens: response.Usage.InputTokensDetails.CachedTokens,
		ReasoningTokens:    response.Usage.OutputTokensDetails.ReasoningTokens,
	}
}

func (response codexCompletedResponse) toChatCompletionResponse(modelName string) model.ChatCompletionResponse {
	textParts := []string{}
	reasoningParts := []string{}
	toolCalls := []model.ChatCompletionToolCall{}

	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if (content.Type == "output_text" || content.Type == "text") && content.Text != "" {
					textParts = append(textParts, content.Text)
				}
			}
		case "reasoning":
			for _, summary := range item.Summary {
				if strings.TrimSpace(summary.Text) != "" {
					reasoningParts = append(reasoningParts, strings.TrimSpace(summary.Text))
				}
			}
		case "function_call":
			callID := firstNonEmpty(item.CallID, item.ID)
			arguments := strings.TrimSpace(item.Arguments)
			if arguments == "" {
				arguments = "{}"
			}
			toolCalls = append(toolCalls, model.ChatCompletionToolCall{
				ID:   callID,
				Type: "function",
				Function: model.ChatCompletionToolCallFunction{
					Name:      strings.TrimSpace(item.Name),
					Arguments: arguments,
				},
			})
		}
	}

	content := strings.Join(textParts, "\n")
	if content == "" && len(toolCalls) == 0 {
		content = response.DeltaText
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else if response.Status == "incomplete" {
		finishReason = "length"
	} else if response.Status == "failed" {
		finishReason = "error"
	}

	reasoning := strings.Join(reasoningParts, "\n")
	reasoningField := ""
	if reasoning != "" {
		reasoningField = "reasoning"
	}

	return model.ChatCompletionResponse{
		Transport:    "http",
		ProviderName: codexEndpointName,
		ModelName:    modelName,
		FinishReason: finishReason,
		Message: model.ChatCompletionMessage{
			Role:           "assistant",
			Content:        content,
			Reasoning:      reasoning,
			ReasoningField: reasoningField,
			ToolCalls:      toolCalls,
		},
		Usage: response.usage(),
	}
}

func convertModelMessagesToCodexInput(messages []model.Message) (string, []map[string]any) {
	chatMessages := make([]model.ChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		chatMessages = append(chatMessages, model.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
			Parts:   message.Parts,
		})
	}
	return convertChatMessagesToCodexInput(chatMessages)
}

func convertChatMessagesToCodexInput(messages []model.ChatCompletionMessage) (string, []map[string]any) {
	instructionParts := []string{}
	items := []map[string]any{}

	for _, message := range messages {
		switch message.Role {
		case "system":
			text := strings.TrimSpace(message.Content)
			if text == "" {
				continue
			}
			if len(items) == 0 {
				instructionParts = append(instructionParts, text)
			} else {
				items = append(items, map[string]any{
					"type": "message",
					"role": "developer",
					"content": []map[string]any{{
						"type": "input_text",
						"text": text,
					}},
				})
			}
		case "user":
			content := codexUserContentBlocks(message.Content, message.Parts)
			if len(content) == 0 {
				continue
			}
			items = append(items, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": content,
			})
		case "assistant":
			if text := strings.TrimSpace(message.Content); text != "" {
				items = append(items, map[string]any{
					"type": "message",
					"role": "assistant",
					"content": []map[string]any{{
						"type": "output_text",
						"text": text,
					}},
				})
			}
			for _, toolCall := range message.ToolCalls {
				arguments := strings.TrimSpace(toolCall.Function.Arguments)
				if arguments == "" {
					arguments = "{}"
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   toolCall.ID,
					"name":      toolCall.Function.Name,
					"arguments": arguments,
				})
			}
		case "tool":
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": message.ToolCallID,
				"output":  message.Content,
			})
		}
	}
	instructions := strings.TrimSpace(strings.Join(instructionParts, "\n\n"))
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	return instructions, items
}

func codexUserContentBlocks(content string, parts []model.MessagePart) []map[string]any {
	blocks := []map[string]any{}
	if text := strings.TrimSpace(content); text != "" {
		blocks = append(blocks, map[string]any{"type": "input_text", "text": text})
	}
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part.Type), "image") && strings.TrimSpace(part.DataBase64) != "" && strings.TrimSpace(part.MimeType) != "" {
			blocks = append(blocks, map[string]any{
				"type":      "input_image",
				"image_url": "data:" + strings.TrimSpace(part.MimeType) + ";base64," + strings.TrimSpace(part.DataBase64),
			})
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			blocks = append(blocks, map[string]any{"type": "input_text", "text": text})
		}
	}
	return blocks
}

func convertChatToolsToCodexTools(tools []model.ChatCompletionTool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{
			"type":   "function",
			"name":   tool.Function.Name,
			"strict": false,
		}
		if description := strings.TrimSpace(tool.Function.Description); description != "" {
			entry["description"] = description
		}
		if parameters := decodeJSONObject(string(tool.Function.Parameters)); parameters != nil {
			entry["parameters"] = parameters
		}
		result = append(result, entry)
	}
	return result
}

func convertCodexToolChoice(rawChoice json.RawMessage) any {
	trimmed := strings.TrimSpace(string(rawChoice))
	if trimmed == "" {
		return nil
	}
	var decoded any
	if json.Unmarshal([]byte(trimmed), &decoded) != nil {
		return nil
	}
	choiceMap, isMap := decoded.(map[string]any)
	if !isMap {
		return decoded
	}
	if choiceType, _ := choiceMap["type"].(string); choiceType == "function" {
		if functionMap, hasFunction := choiceMap["function"].(map[string]any); hasFunction {
			if name, _ := functionMap["name"].(string); strings.TrimSpace(name) != "" {
				return map[string]any{
					"type": "function",
					"name": strings.TrimSpace(name),
				}
			}
		}
	}
	return decoded
}

func discoverCodexModels(ctx context.Context) ([]string, error) {
	credentials, authError := loadCodexCredentials()
	if authError != nil {
		return append([]string{}, codexCatalogModels...), nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if credentials.isExpiringSoon(time.Now()) && strings.TrimSpace(credentials.RefreshToken) != "" {
		if refreshed, refreshError := refreshCodexCredentials(ctx, client, codexOAuthTokenURL, credentials); refreshError == nil {
			credentials = refreshed
		}
	}
	models, fetchError := fetchCodexModels(ctx, client, codexModelsURL+"?client_version="+codexClientVersion, credentials)
	if fetchError != nil || len(models) == 0 {
		return append([]string{}, codexCatalogModels...), nil
	}
	return models, nil
}

func fetchCodexModels(ctx context.Context, client *http.Client, targetURL string, credentials codexCredentials) ([]string, error) {
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if errorValue != nil {
		return nil, errorValue
	}
	request.Header.Set("Authorization", "Bearer "+credentials.AccessToken)
	request.Header.Set("originator", codexOAuthOriginator)
	if credentials.AccountID != "" {
		request.Header.Set("chatgpt-account-id", credentials.AccountID)
	}
	response, errorValue := client.Do(request)
	if errorValue != nil {
		return nil, errorValue
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex models returned %d", response.StatusCode)
	}
	body, errorValue := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if errorValue != nil {
		return nil, errorValue
	}
	return parseCodexModelsResponse(body)
}

func parseCodexModelsResponse(body []byte) ([]string, error) {
	var payload struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
			Priority   int    `json:"priority"`
		} `json:"models"`
	}
	if errorValue := json.Unmarshal(body, &payload); errorValue != nil {
		return nil, errorValue
	}
	type rankedModel struct {
		slug     string
		priority int
		order    int
	}
	seen := map[string]bool{}
	ranked := make([]rankedModel, 0, len(payload.Models))
	for index, entry := range payload.Models {
		slug := strings.TrimSpace(entry.Slug)
		if slug == "" || seen[slug] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(entry.Visibility), "hide") {
			continue
		}
		seen[slug] = true
		ranked = append(ranked, rankedModel{slug: slug, priority: entry.Priority, order: index})
	}
	sort.SliceStable(ranked, func(left int, right int) bool {
		if ranked[left].priority != ranked[right].priority {
			return ranked[left].priority < ranked[right].priority
		}
		return ranked[left].order < ranked[right].order
	})
	models := make([]string, 0, len(ranked))
	for _, entry := range ranked {
		models = append(models, entry.slug)
	}
	return models, nil
}
