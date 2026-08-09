package openaicompatible

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type Provider struct {
	endpointURL string
	apiKey      string
	modelName   string
	httpClient  *http.Client
}

func NewProvider(endpointURL string, apiKey string, modelName string) *Provider {
	return &Provider{
		endpointURL: strings.TrimSuffix(strings.TrimSpace(endpointURL), "/"),
		apiKey:      strings.TrimSpace(apiKey),
		modelName:   strings.TrimSpace(modelName),
		httpClient:  http.DefaultClient,
	}
}

func (provider *Provider) UseHTTPClient(httpClient *http.Client) {
	provider.httpClient = httpClient
}

func (provider *Provider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	response, errorValue := provider.complete(ctx, []model.Message{{Role: "user", Content: prompt}}, nil)
	if errorValue != nil {
		return "", errorValue
	}
	return response.Content, nil
}

func (provider *Provider) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return provider.complete(ctx, request.Messages, &request.StructuredOutputSchema)
}

func (provider *Provider) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	body, errorValue := json.Marshal(chatCompletionRequest(provider.modelName, request))
	if errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	responseBody, errorValue := provider.post(ctx, body)
	if errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	return decodeChatCompletion(responseBody, provider.modelName)
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
	if request.GenerationOptions.MaxTokens != nil {
		chatRequest["max_tokens"] = *request.GenerationOptions.MaxTokens
	}
	return chatRequest
}

func chatCompletionMessages(messages []model.ChatCompletionMessage) []map[string]any {
	chat := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		entry := map[string]any{"role": message.Role, "content": messageText(message.Content, message.Parts)}
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
				Role      string     `json:"role"`
				Content   string     `json:"content"`
				ToolCalls []toolCall `json:"tool_calls"`
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
	return model.ChatCompletionResponse{
		Transport:    "http",
		ProviderName: "openai-compatible",
		ModelName:    modelName,
		FinishReason: decoded.Choices[0].FinishReason,
		Message: model.ChatCompletionMessage{
			Role:      decoded.Choices[0].Message.Role,
			Content:   decoded.Choices[0].Message.Content,
			ToolCalls: chatCompletionToolCalls(decoded.Choices[0].Message.ToolCalls),
		},
		Usage: decoded.Usage.measured(),
	}, nil
}

func (provider *Provider) complete(ctx context.Context, messages []model.Message, schema *model.StructuredOutputSchema) (model.StructuredResponse, error) {
	body, errorValue := json.Marshal(completionRequest(provider.modelName, messages, schema))
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	responseBody, errorValue := provider.post(ctx, body)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	return decodeCompletion(responseBody, provider.modelName)
}

func (provider *Provider) post(ctx context.Context, body []byte) ([]byte, error) {
	httpRequest, errorValue := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpointURL+"/chat/completions", bytes.NewReader(body))
	if errorValue != nil {
		return nil, errorValue
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if provider.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	}

	httpResponse, errorValue := provider.httpClient.Do(httpRequest)
	if errorValue != nil {
		return nil, errorValue
	}
	defer httpResponse.Body.Close()

	responseBody, errorValue := io.ReadAll(httpResponse.Body)
	if errorValue != nil {
		return nil, errorValue
	}
	if httpResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model endpoint returned %d: %s", httpResponse.StatusCode, truncated(string(responseBody)))
	}
	return responseBody, nil
}

func completionRequest(modelName string, messages []model.Message, schema *model.StructuredOutputSchema) map[string]any {
	request := map[string]any{
		"model":    modelName,
		"messages": chatMessages(messages),
	}
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
