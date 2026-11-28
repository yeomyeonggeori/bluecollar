package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

const (
	geminiCLIEndpointName      = "gemini-api"
	defaultGeminiCLIModelName  = "gemini-4-hydrogen"
	geminiDefaultContextWindow = 1048576
	geminiThoughtSignatureSlot = "gemini_thought_signature"
	geminiCLIMaxRetries        = 3
	geminiCLIRetryBaseDelay    = time.Second
)

type geminiCLIProvider struct {
	binaryPath     string
	modelName      string
	apiKey         string
	retryBaseDelay time.Duration
	runner         func(ctx context.Context, binaryPath string, args []string, stdin []byte) ([]byte, []byte, error)
}

func newGeminiCLIProvider(modelName string, apiKey string) *geminiCLIProvider {
	resolvedModel := strings.TrimSpace(modelName)
	if resolvedModel == "" {
		resolvedModel = defaultGeminiCLIModelName
	}
	return &geminiCLIProvider{
		binaryPath:     geminiCLIEndpointName,
		modelName:      resolvedModel,
		apiKey:         strings.TrimSpace(apiKey),
		retryBaseDelay: geminiCLIRetryBaseDelay,
		runner:         runGeminiCLICommand,
	}
}

func isGeminiCLIEndpoint(endpointURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(endpointURL))
	return normalized == geminiCLIEndpointName || normalized == "gemini-cli" || normalized == "gemini"
}

func geminiCLIBinaryAvailable() bool {
	_, errorValue := exec.LookPath(geminiCLIEndpointName)
	return errorValue == nil
}

func (provider *geminiCLIProvider) ContextWindowTokens(context.Context) int {
	return geminiDefaultContextWindow
}

func (provider *geminiCLIProvider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	requestBody := map[string]any{
		"model":  provider.modelName,
		"stream": false,
		"store":  false,
		"input":  prompt,
	}
	interaction, errorValue := provider.invokeInteraction(ctx, requestBody)
	if errorValue != nil {
		return "", errorValue
	}
	return extractInteractionText(interaction), nil
}

func (provider *geminiCLIProvider) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	systemInstruction, steps := convertModelMessagesToInteractionSteps(request.Messages)
	requestBody := map[string]any{
		"model":  provider.modelName,
		"stream": false,
		"store":  false,
		"input":  steps,
	}
	if systemInstruction != "" {
		requestBody["system_instruction"] = systemInstruction
	}
	if schema := decodeJSONObject(request.StructuredOutputSchema.Document); schema != nil {
		requestBody["response_format"] = map[string]any{
			"type":      "text",
			"mime_type": "application/json",
			"schema":    sanitizeSchemaForGemini(schema),
		}
	}
	if generationConfig := buildGeminiGenerationConfig(request.GenerationOptions, nil); len(generationConfig) > 0 {
		requestBody["generation_config"] = generationConfig
	}
	interaction, errorValue := provider.invokeInteraction(ctx, requestBody)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	return model.StructuredResponse{
		Content:      extractInteractionText(interaction),
		ProviderName: geminiCLIEndpointName,
		ModelName:    provider.modelName,
		Usage:        interaction.usage(),
	}, nil
}

func (provider *geminiCLIProvider) GenerateChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	systemInstruction, steps := convertChatMessagesToInteractionSteps(request.Messages)
	requestBody := map[string]any{
		"model":  provider.modelName,
		"stream": false,
		"store":  false,
		"input":  steps,
	}
	if systemInstruction != "" {
		requestBody["system_instruction"] = systemInstruction
	}
	if tools := convertChatToolsToGeminiFunctions(request.Tools); len(tools) > 0 {
		requestBody["tools"] = tools
	}
	if generationConfig := buildGeminiGenerationConfig(request.GenerationOptions, request.ToolChoice); len(generationConfig) > 0 {
		requestBody["generation_config"] = generationConfig
	}
	interaction, errorValue := provider.invokeInteraction(ctx, requestBody)
	if errorValue != nil {
		return model.ChatCompletionResponse{}, errorValue
	}
	return decodeGeminiInteractionToChatResponse(interaction, provider.modelName), nil
}

func (provider *geminiCLIProvider) GenerateRecoveryChatCompletion(ctx context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return provider.GenerateChatCompletion(ctx, request)
}

func (provider *geminiCLIProvider) invokeInteraction(ctx context.Context, requestBody map[string]any) (geminiInteractionResponse, error) {
	encodedBody, errorValue := json.Marshal(requestBody)
	if errorValue != nil {
		return geminiInteractionResponse{}, errorValue
	}
	args := []string{
		"generate",
		"--no-interactive",
		"--output-format", "json",
		"--body", "@-",
	}
	if provider.apiKey != "" {
		args = append(args, "--api-key", provider.apiKey)
	}
	var stdout, stderr []byte
	var runError error
	for attempt := 0; ; attempt++ {
		stdout, stderr, runError = provider.runner(ctx, provider.binaryPath, args, encodedBody)
		model.RecordWireExchange(ctx, model.WireExchange{
			Endpoint: "gemini-api://interactions",
			Request:  string(encodedBody),
			Response: string(stdout),
		})
		if runError == nil {
			break
		}
		detail, isTransient := parseGeminiCLIError(stderr, stdout, runError)
		if !isTransient || attempt >= geminiCLIMaxRetries || ctx.Err() != nil {
			return geminiInteractionResponse{}, fmt.Errorf("gemini-api failed: %s", detail)
		}
		delay := provider.retryBaseDelay << attempt
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return geminiInteractionResponse{}, fmt.Errorf("gemini-api failed: %s", detail)
		case <-timer.C:
		}
	}
	var interaction geminiInteractionResponse
	if decodeError := json.Unmarshal(bytes.TrimSpace(stdout), &interaction); decodeError != nil {
		return geminiInteractionResponse{}, fmt.Errorf("could not parse gemini-api JSON response: %w (%s)", decodeError, clippedTo(strings.TrimSpace(string(stdout)), 200))
	}
	if interaction.Error != nil && strings.TrimSpace(interaction.Error.Message) != "" {
		return geminiInteractionResponse{}, errors.New("gemini-api error: " + interaction.Error.Message)
	}
	return interaction, nil
}

func parseGeminiCLIError(stderr []byte, stdout []byte, runError error) (string, bool) {
	for _, raw := range [][]byte{stderr, stdout} {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		var payload struct {
			Message    string          `json:"message"`
			Error      json.RawMessage `json:"error"`
			ErrorType  string          `json:"error_type"`
			StatusCode int             `json:"status_code"`
		}
		if json.Unmarshal(trimmed, &payload) == nil && (payload.Message != "" || len(payload.Error) > 0 || payload.StatusCode > 0) {
			isTransient := payload.StatusCode == 408 || payload.StatusCode == 429 || payload.StatusCode >= 500 || payload.ErrorType == "server_error"
			message := strings.TrimSpace(payload.Message)
			code := ""
			if len(payload.Error) > 0 {
				var stringError string
				if json.Unmarshal(payload.Error, &stringError) == nil && strings.TrimSpace(stringError) != "" {
					message = firstNonEmpty(message, stringError)
				} else {
					var nested struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					}
					if json.Unmarshal(payload.Error, &nested) == nil {
						code = strings.TrimSpace(nested.Code)
						message = firstNonEmpty(nested.Message, message)
						if code == "service_unavailable" || code == "rate_limit_exceeded" || code == "resource_exhausted" {
							isTransient = true
						}
					}
				}
			}
			prefixParts := []string{}
			if payload.StatusCode > 0 {
				prefixParts = append(prefixParts, strconv.Itoa(payload.StatusCode))
			}
			if code != "" {
				prefixParts = append(prefixParts, code)
			}
			if message == "" {
				message = collapsedWhitespace(string(trimmed))
			}
			if len(prefixParts) > 0 {
				return strings.Join(prefixParts, " ") + ": " + collapsedWhitespace(message), isTransient
			}
			return collapsedWhitespace(message), isTransient
		}
		return collapsedWhitespace(string(trimmed)), false
	}
	return runError.Error(), false
}

func runGeminiCLICommand(ctx context.Context, binaryPath string, args []string, stdin []byte) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binaryPath, args...)
	if len(stdin) > 0 {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	errorValue := command.Run()
	return stdout.Bytes(), stderr.Bytes(), errorValue
}

func discoverGeminiCLIModels(ctx context.Context, apiKey string) ([]string, error) {
	args := []string{"models", "list", "--no-interactive", "--output-format", "json"}
	if strings.TrimSpace(apiKey) != "" {
		args = append(args, "--api-key", strings.TrimSpace(apiKey))
	}
	stdout, _, errorValue := runGeminiCLICommand(ctx, geminiCLIEndpointName, args, nil)
	if errorValue == nil {
		if models := parseGeminiCLIModelsJSON(stdout); len(models) > 0 {
			return models, nil
		}
	}
	catalogStdout, catalogStderr, catalogError := runGeminiCLICommand(ctx, geminiCLIEndpointName, []string{"models", "--no-interactive", "--output-format", "json"}, nil)
	if catalogError == nil {
		if models := parseGeminiCLIModelsJSON(catalogStdout); len(models) > 0 {
			return models, nil
		}
	}
	plainStdout, _, plainError := runGeminiCLICommand(ctx, geminiCLIEndpointName, []string{"models", "--no-interactive"}, nil)
	if plainError == nil {
		if models := parseGeminiCLIModelsText(string(plainStdout)); len(models) > 0 {
			return models, nil
		}
	}
	return nil, errors.New(firstNonEmpty(strings.TrimSpace(string(catalogStderr)), "could not list models from gemini-api"))
}

func parseGeminiCLIModelsJSON(data []byte) []string {
	var raw any
	if json.Unmarshal(bytes.TrimSpace(data), &raw) != nil {
		return nil
	}
	seen := map[string]bool{}
	models := []string{}
	add := func(candidate string) {
		trimmed := strings.TrimPrefix(strings.TrimSpace(candidate), "models/")
		if trimmed == "" || seen[trimmed] {
			return
		}
		seen[trimmed] = true
		models = append(models, trimmed)
	}
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if name, isString := typed["name"].(string); isString && strings.HasPrefix(name, "models/") {
				add(name)
			}
			if id, isString := typed["id"].(string); isString && (strings.HasPrefix(id, "gemini-") || strings.HasPrefix(id, "gemma-") || strings.HasPrefix(id, "lyria-")) {
				add(id)
			}
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(raw)
	return models
}

func parseGeminiCLIModelsText(text string) []string {
	seen := map[string]bool{}
	models := []string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		candidate := fields[0]
		if !strings.HasPrefix(candidate, "gemini-") && !strings.HasPrefix(candidate, "gemma-") {
			continue
		}
		if !seen[candidate] {
			seen[candidate] = true
			models = append(models, candidate)
		}
	}
	return models
}

type geminiInteractionResponse struct {
	ID      string                  `json:"id"`
	Status  string                  `json:"status"`
	Outputs []geminiInteractionStep `json:"outputs"`
	Steps   []geminiInteractionStep `json:"steps"`
	Usage   *geminiInteractionUsage `json:"usage"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type geminiInteractionStep struct {
	Type      string                     `json:"type"`
	Text      string                     `json:"text"`
	Signature string                     `json:"signature"`
	Summary   []geminiInteractionContent `json:"summary"`
	Content   []geminiInteractionContent `json:"content"`
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Arguments json.RawMessage            `json:"arguments"`
}

type geminiInteractionContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type geminiInteractionUsage struct {
	TotalInputTokens   int64 `json:"total_input_tokens"`
	TotalOutputTokens  int64 `json:"total_output_tokens"`
	TotalCachedTokens  int64 `json:"total_cached_tokens"`
	TotalThoughtTokens int64 `json:"total_thought_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
}

func (response geminiInteractionResponse) allOutputs() []geminiInteractionStep {
	if len(response.Outputs) > 0 {
		return response.Outputs
	}
	return response.Steps
}

func (response geminiInteractionResponse) usage() model.Usage {
	if response.Usage == nil {
		return model.Usage{}
	}
	total := response.Usage.TotalTokens
	if total == 0 {
		total = response.Usage.TotalInputTokens + response.Usage.TotalOutputTokens
	}
	return model.Usage{
		PromptTokens:       response.Usage.TotalInputTokens,
		CompletionTokens:   response.Usage.TotalOutputTokens,
		TotalTokens:        total,
		CachedPromptTokens: response.Usage.TotalCachedTokens,
		ReasoningTokens:    response.Usage.TotalThoughtTokens,
	}
}

func extractInteractionText(response geminiInteractionResponse) string {
	parts := []string{}
	for _, step := range response.allOutputs() {
		switch step.Type {
		case "text":
			if step.Text != "" {
				parts = append(parts, step.Text)
			}
		case "model_output":
			for _, content := range step.Content {
				if content.Type == "text" && content.Text != "" {
					parts = append(parts, content.Text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func decodeGeminiInteractionToChatResponse(response geminiInteractionResponse, modelName string) model.ChatCompletionResponse {
	textParts := []string{}
	toolCalls := []model.ChatCompletionToolCall{}
	thoughtSignature := ""
	thoughtSummary := []string{}

	for index, step := range response.allOutputs() {
		switch step.Type {
		case "thought":
			if step.Signature != "" {
				thoughtSignature = step.Signature
			}
			for _, item := range step.Summary {
				if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
					thoughtSummary = append(thoughtSummary, strings.TrimSpace(item.Text))
				}
			}
		case "text":
			if step.Text != "" {
				textParts = append(textParts, step.Text)
			}
		case "model_output":
			for _, item := range step.Content {
				if item.Type == "text" && item.Text != "" {
					textParts = append(textParts, item.Text)
				}
			}
		case "function_call":
			callID := strings.TrimSpace(step.ID)
			if callID == "" {
				callID = "call-" + strconv.Itoa(index+1)
			}
			arguments := "{}"
			if len(bytes.TrimSpace(step.Arguments)) > 0 {
				arguments = string(bytes.TrimSpace(step.Arguments))
			}
			toolCalls = append(toolCalls, model.ChatCompletionToolCall{
				ID:   callID,
				Type: "function",
				Function: model.ChatCompletionToolCallFunction{
					Name:      strings.TrimSpace(step.Name),
					Arguments: arguments,
				},
			})
		}
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	} else if strings.EqualFold(response.Status, "failed") {
		finishReason = "error"
	}

	reasoning := ""
	reasoningField := ""
	if thoughtSignature != "" {
		reasoning = thoughtSignature
		reasoningField = geminiThoughtSignatureSlot
	} else if len(thoughtSummary) > 0 {
		reasoning = strings.Join(thoughtSummary, "\n")
		reasoningField = "reasoning"
	}

	return model.ChatCompletionResponse{
		Transport:    "cli",
		ProviderName: geminiCLIEndpointName,
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

func convertModelMessagesToInteractionSteps(messages []model.Message) (string, []map[string]any) {
	chatMessages := make([]model.ChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		chatMessages = append(chatMessages, model.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
			Parts:   message.Parts,
		})
	}
	return convertChatMessagesToInteractionSteps(chatMessages)
}

func convertChatMessagesToInteractionSteps(messages []model.ChatCompletionMessage) (string, []map[string]any) {
	systemParts := []string{}
	steps := []map[string]any{}
	toolNamesByCallID := map[string]string{}

	for _, message := range messages {
		switch message.Role {
		case "system":
			text := strings.TrimSpace(message.Content)
			if text == "" {
				continue
			}
			if len(steps) == 0 {
				systemParts = append(systemParts, text)
			} else {
				steps = append(steps, map[string]any{
					"type": "user_input",
					"content": []map[string]any{{
						"type": "text",
						"text": text,
					}},
				})
			}
		case "user":
			content := geminiContentBlocks(message.Content, message.Parts)
			if len(content) == 0 {
				continue
			}
			steps = append(steps, map[string]any{
				"type":    "user_input",
				"content": content,
			})
		case "assistant":
			if message.Reasoning != "" && message.ReasoningField == geminiThoughtSignatureSlot {
				steps = append(steps, map[string]any{
					"type":      "thought",
					"signature": message.Reasoning,
				})
			}
			if text := strings.TrimSpace(message.Content); text != "" {
				steps = append(steps, map[string]any{
					"type": "model_output",
					"content": []map[string]any{{
						"type": "text",
						"text": text,
					}},
				})
			}
			for _, toolCall := range message.ToolCalls {
				toolNamesByCallID[toolCall.ID] = toolCall.Function.Name
				arguments := decodeJSONObject(toolCall.Function.Arguments)
				if arguments == nil {
					arguments = map[string]any{}
				}
				steps = append(steps, map[string]any{
					"type":      "function_call",
					"id":        toolCall.ID,
					"name":      toolCall.Function.Name,
					"arguments": arguments,
				})
			}
		case "tool":
			step := map[string]any{
				"type":    "function_result",
				"call_id": message.ToolCallID,
				"result": []map[string]any{{
					"type": "text",
					"text": message.Content,
				}},
			}
			if toolName := toolNamesByCallID[message.ToolCallID]; toolName != "" {
				step["name"] = toolName
			}
			steps = append(steps, step)
		}
	}
	return strings.Join(systemParts, "\n\n"), steps
}

func geminiContentBlocks(content string, parts []model.MessagePart) []map[string]any {
	blocks := []map[string]any{}
	if text := strings.TrimSpace(content); text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part.Type), "image") && strings.TrimSpace(part.DataBase64) != "" && strings.TrimSpace(part.MimeType) != "" {
			blocks = append(blocks, map[string]any{
				"type":      "image",
				"mime_type": strings.TrimSpace(part.MimeType),
				"data":      strings.TrimSpace(part.DataBase64),
			})
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
		}
	}
	return blocks
}

func convertChatToolsToGeminiFunctions(tools []model.ChatCompletionTool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		entry := map[string]any{
			"type": "function",
			"name": tool.Function.Name,
		}
		if description := strings.TrimSpace(tool.Function.Description); description != "" {
			entry["description"] = description
		}
		if parameters := decodeJSONObject(string(tool.Function.Parameters)); parameters != nil {
			entry["parameters"] = sanitizeSchemaForGemini(parameters)
		}
		result = append(result, entry)
	}
	return result
}

func buildGeminiGenerationConfig(options model.GenerationOptions, toolChoice json.RawMessage) map[string]any {
	config := map[string]any{}
	if options.MaxTokens != nil {
		config["max_output_tokens"] = *options.MaxTokens
	}
	if options.Seed != nil {
		config["seed"] = *options.Seed
	}
	if options.Temperature != nil {
		config["temperature"] = *options.Temperature
	}
	if trimmedChoice := strings.TrimSpace(string(toolChoice)); trimmedChoice != "" {
		switch trimmedChoice {
		case `"required"`:
			config["tool_choice"] = "any"
		case `"auto"`:
			config["tool_choice"] = "auto"
		case `"none"`:
			config["tool_choice"] = "none"
		}
	}
	return config
}

func decodeJSONObject(document string) map[string]any {
	trimmed := strings.TrimSpace(document)
	if trimmed == "" {
		return nil
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(trimmed), &decoded) != nil {
		return nil
	}
	return decoded
}

func sanitizeSchemaForGemini(node any) any {
	switch typed := node.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(typed))
		for key, value := range typed {
			if key == "$schema" {
				continue
			}
			cleaned[key] = sanitizeSchemaForGemini(value)
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(typed))
		for index, item := range typed {
			cleaned[index] = sanitizeSchemaForGemini(item)
		}
		return cleaned
	default:
		return node
	}
}
