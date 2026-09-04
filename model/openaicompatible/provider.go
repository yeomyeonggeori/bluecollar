package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type Provider struct {
	endpointURL    string
	apiKey         string
	modelName      string
	httpClient     *http.Client
	retryBaseDelay time.Duration
}

func NewProvider(endpointURL string, apiKey string, modelName string) *Provider {
	return &Provider{
		endpointURL:    strings.TrimSuffix(strings.TrimSpace(endpointURL), "/"),
		apiKey:         strings.TrimSpace(apiKey),
		modelName:      strings.TrimSpace(modelName),
		httpClient:     http.DefaultClient,
		retryBaseDelay: transientRetryBaseDelay,
	}
}

func (provider *Provider) UseHTTPClient(httpClient *http.Client) {
	provider.httpClient = httpClient
}

func (provider *Provider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	response, errorValue := provider.complete(ctx, []model.Message{{Role: "user", Content: prompt}}, nil, model.GenerationOptions{})
	if errorValue != nil {
		return "", errorValue
	}
	return response.Content, nil
}

func (provider *Provider) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return provider.complete(ctx, request.Messages, &request.StructuredOutputSchema, request.GenerationOptions)
}

func (provider *Provider) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	body, errorValue := json.Marshal(chatCompletionRequest(provider.modelName, request))
	if errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	for attempt := 0; ; attempt++ {
		responseBody, postError := provider.post(ctx, body)
		if postError != nil {
			return model.ChatCompletionResponse{}, postError
		}
		response, decodeError := decodeChatCompletion(responseBody, provider.modelName)
		if decodeError != nil {
			return model.ChatCompletionResponse{}, decodeError
		}
		if response.FinishReason != "error" || attempt >= transientRetryCount || ctx.Err() != nil {
			return response, nil
		}
		if waitBeforeRetry(ctx, retryDelay(provider.retryBaseDelay, attempt, 0)) != nil {
			return response, nil
		}
	}
}

func (provider *Provider) GenerateRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return provider.GenerateChatCompletion(ctx, request)
}

func chatCompletionRequest(modelName string, request model.ChatCompletionRequest) map[string]any {
	chatRequest := map[string]any{
		"model":    modelName,
		"messages": chatCompletionMessages(request.Messages),
	}
	if len(request.Tools) > 0 {
		chatRequest["tools"] = request.Tools
	}
	if len(request.ToolChoice) > 0 {
		chatRequest["tool_choice"] = json.RawMessage(request.ToolChoice)
	}
	chatRequest["parallel_tool_calls"] = request.ParallelToolCalls
	chatRequest["usage"] = map[string]any{"include": true}
	applyGenerationOptions(chatRequest, request.GenerationOptions)
	return chatRequest
}

func applyGenerationOptions(chatRequest map[string]any, generationOptions model.GenerationOptions) {
	if generationOptions.MaxTokens != nil {
		chatRequest["max_tokens"] = *generationOptions.MaxTokens
	}
	if generationOptions.Seed != nil {
		chatRequest["seed"] = *generationOptions.Seed
	}
	if generationOptions.Temperature != nil {
		chatRequest["temperature"] = *generationOptions.Temperature
	}
}

func chatCompletionMessages(messages []model.ChatCompletionMessage) []map[string]any {
	chat := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		entry := map[string]any{"role": message.Role, "content": chatCompletionContent(message.Content, message.Parts)}
		if message.Role == "assistant" && message.Reasoning != "" {
			entry[reasoningFieldName(message.ReasoningField)] = message.Reasoning
		}
		if message.ToolCallID != "" {
			entry["tool_call_id"] = message.ToolCallID
		}
		if len(message.ToolCalls) > 0 {
			entry["tool_calls"] = message.ToolCalls
		}
		chat = append(chat, entry)
	}
	return chat
}

func reasoningFieldName(fieldName string) string {
	switch fieldName {
	case "reasoning", "reasoning_content":
		return fieldName
	default:
		return "reasoning_content"
	}
}

type reportedUsage struct {
	PromptTokens        int64   `json:"prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	Cost                float64 `json:"cost"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (usage reportedUsage) measured() model.Usage {
	return model.Usage{
		PromptTokens:       usage.PromptTokens,
		CompletionTokens:   usage.CompletionTokens,
		TotalTokens:        usage.TotalTokens,
		CostUSD:            usage.Cost,
		CachedPromptTokens: usage.PromptTokensDetails.CachedTokens,
	}
}

func decodeChatCompletion(responseBody []byte, modelName string) (model.ChatCompletionResponse, error) {
	var decoded struct {
		Choices []struct {
			Message struct {
				Role             string     `json:"role"`
				Content          string     `json:"content"`
				Reasoning        string     `json:"reasoning"`
				ReasoningContent string     `json:"reasoning_content"`
				ToolCalls        []toolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage reportedUsage `json:"usage"`
	}
	if errorValue := json.Unmarshal(responseBody, &decoded); errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	if len(decoded.Choices) == 0 {
		return model.ChatCompletionResponse{}, errors.New("model endpoint returned no choices")
	}
	reasoning, reasoningField := decoded.Choices[0].Message.ReasoningContent, "reasoning_content"
	if reasoning == "" {
		reasoning, reasoningField = decoded.Choices[0].Message.Reasoning, "reasoning"
	}
	if reasoning == "" {
		reasoningField = ""
	}
	finishReason := decoded.Choices[0].FinishReason
	if finishReason == "" && len(decoded.Choices[0].Message.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return model.ChatCompletionResponse{
		Transport:    "http",
		ProviderName: "openai-compatible",
		ModelName:    modelName,
		FinishReason: finishReason,
		Message: model.ChatCompletionMessage{
			Role:           decoded.Choices[0].Message.Role,
			Content:        decoded.Choices[0].Message.Content,
			Reasoning:      reasoning,
			ReasoningField: reasoningField,
			ToolCalls:      chatCompletionToolCalls(decoded.Choices[0].Message.ToolCalls),
		},
		Usage: decoded.Usage.measured(),
	}, nil
}

func (provider *Provider) complete(ctx context.Context, messages []model.Message, schema *model.StructuredOutputSchema, generationOptions model.GenerationOptions) (model.StructuredResponse, error) {
	body, errorValue := json.Marshal(completionRequest(provider.modelName, messages, schema, generationOptions))
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	responseBody, errorValue := provider.post(ctx, body)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	return decodeCompletion(responseBody, provider.modelName)
}

const (
	transientRetryCount        = 3
	transientRetryBaseDelay    = time.Second
	transientRetryDelayCeiling = 30 * time.Second
)

func (provider *Provider) post(ctx context.Context, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		responseBody, attemptOutcome, errorValue := provider.postOnce(ctx, body)
		if errorValue == nil {
			return responseBody, nil
		}
		if attempt >= transientRetryCount || !attemptOutcome.isTransient || ctx.Err() != nil {
			return nil, errorValue
		}
		if waitError := waitBeforeRetry(ctx, retryDelay(provider.retryBaseDelay, attempt, attemptOutcome.retryAfter)); waitError != nil {
			return nil, errorValue
		}
	}
}

type postAttemptOutcome struct {
	isTransient bool
	retryAfter  time.Duration
}

func (provider *Provider) postOnce(ctx context.Context, body []byte) ([]byte, postAttemptOutcome, error) {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpointURL+"/chat/completions", bytes.NewReader(body))
	if errorValue != nil {
		return nil, postAttemptOutcome{}, errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	setAttributionHeaders(httpRequest)
	if provider.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	}

	httpResponse, errorValue := provider.httpClient.Do(httpRequest)
	if errorValue != nil {
		return nil, postAttemptOutcome{isTransient: ctx.Err() == nil}, errorValue
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return nil, postAttemptOutcome{isTransient: ctx.Err() == nil}, errorValue
	}
	if httpResponse.StatusCode != http.StatusOK {
		return nil, postAttemptOutcome{
			isTransient: isTransientStatus(httpResponse.StatusCode),
			retryAfter:  retryAfterHeaderDelay(httpResponse.Header.Get("Retry-After")),
		}, fmt.Errorf("model endpoint returned %d: %s", httpResponse.StatusCode, truncated(string(responseBody)))
	}
	return responseBody, postAttemptOutcome{}, nil
}

func setAttributionHeaders(httpRequest *http.Request) {
	httpRequest.Header.Set("HTTP-Referer", "https://github.com/yeomyeonggeori/bluecollar")
	httpRequest.Header.Set("X-Title", "bluecollar")
}

func isTransientStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

func retryAfterHeaderDelay(headerValue string) time.Duration {
	seconds, errorValue := strconv.Atoi(strings.TrimSpace(headerValue))
	if errorValue != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func retryDelay(baseDelay time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	delay := baseDelay << attempt
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > transientRetryDelayCeiling {
		delay = transientRetryDelayCeiling
	}
	return delay
}

func waitBeforeRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func completionRequest(modelName string, messages []model.Message, schema *model.StructuredOutputSchema, generationOptions model.GenerationOptions) map[string]any {
	request := map[string]any{
		"model":    modelName,
		"messages": chatMessages(messages),
	}
	applyGenerationOptions(request, generationOptions)
	if schema == nil || strings.TrimSpace(schema.Document) == "" {
		return request
	}
	var schemaDocument any
	if json.Unmarshal([]byte(schema.Document), &schemaDocument) != nil {
		return request
	}
	request["tools"] = []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":       schema.Name,
			"parameters": schemaDocument,
		},
	}}
	request["tool_choice"] = map[string]any{
		"type":     "function",
		"function": map[string]any{"name": schema.Name},
	}
	request["usage"] = map[string]any{"include": true}
	return request
}

func chatMessages(messages []model.Message) []map[string]string {
	chat := make([]map[string]string, 0, len(messages))
	for _, message := range messages {
		chat = append(chat, map[string]string{"role": message.Role, "content": messageText(message.Content, message.Parts)})
	}
	return chat
}

func chatCompletionContent(content string, parts []model.MessagePart) any {
	if !partsCarryAnImage(parts) {
		return messageText(content, parts)
	}
	contentParts := []map[string]any{}
	if text := strings.TrimSpace(content); text != "" {
		contentParts = append(contentParts, map[string]any{"type": "text", "text": text})
	}
	for _, part := range parts {
		if isImagePart(part) {
			contentParts = append(contentParts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": "data:" + part.MimeType + ";base64," + part.DataBase64},
			})
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			contentParts = append(contentParts, map[string]any{"type": "text", "text": text})
		}
	}
	return contentParts
}

func partsCarryAnImage(parts []model.MessagePart) bool {
	for _, part := range parts {
		if isImagePart(part) {
			return true
		}
	}
	return false
}

func isImagePart(part model.MessagePart) bool {
	return strings.HasPrefix(strings.TrimSpace(part.MimeType), "image/") && strings.TrimSpace(part.DataBase64) != ""
}

func messageText(content string, parts []model.MessagePart) string {
	for _, part := range parts {
		content += part.Text
	}
	return content
}

func decodeCompletion(responseBody []byte, modelName string) (model.StructuredResponse, error) {
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []toolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage reportedUsage `json:"usage"`
	}
	if errorValue := json.Unmarshal(responseBody, &decoded); errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	if len(decoded.Choices) == 0 {
		return model.StructuredResponse{}, errors.New("model endpoint returned no choices")
	}
	arguments, hasToolCall := firstToolCallArguments(decoded.Choices[0].Message.ToolCalls)
	if !hasToolCall {
		return model.StructuredResponse{}, errors.New("model answered " + decoded.Choices[0].FinishReason + " with prose instead of calling the schema it was given: " + truncated(decoded.Choices[0].Message.Content))
	}
	return model.StructuredResponse{
		Transport:    "http",
		ProviderName: "openai-compatible",
		ModelName:    modelName,
		Content:      arguments,
		FinishReason: decoded.Choices[0].FinishReason,
		Usage:        decoded.Usage.measured(),
	}, nil
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func chatCompletionToolCalls(toolCalls []toolCall) []model.ChatCompletionToolCall {
	calls := make([]model.ChatCompletionToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		calls = append(calls, model.ChatCompletionToolCall{
			ID:   toolCall.ID,
			Type: toolCall.Type,
			Function: model.ChatCompletionToolCallFunction{
				Name:      toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			},
		})
	}
	return calls
}

func firstToolCallArguments(toolCalls []toolCall) (string, bool) {
	for _, toolCall := range toolCalls {
		if arguments := strings.TrimSpace(toolCall.Function.Arguments); arguments != "" {
			return arguments, true
		}
	}
	return "", false
}

func truncated(text string) string {
	const limit = 300
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "…"
}

func (provider *Provider) ContextWindowTokens(ctx context.Context) int {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, provider.endpointURL+"/models", nil)
	if errorValue != nil {
		return 0
	}
	setAttributionHeaders(httpRequest)
	if provider.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	}
	httpResponse, errorValue := provider.httpClient.Do(httpRequest)
	if errorValue != nil {
		return 0
	}
	defer httpResponse.Body.Close()
	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil || httpResponse.StatusCode != http.StatusOK {
		return 0
	}
	var catalogue struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if json.Unmarshal(responseBody, &catalogue) != nil {
		return 0
	}
	for _, entry := range catalogue.Data {
		if entry.ID == provider.modelName {
			return entry.ContextLength
		}
	}
	return 0
}
