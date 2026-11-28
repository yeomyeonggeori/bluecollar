package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultLocalEndpointURL = "http://127.0.0.1:11434/v1"
	modelDiscoveryTimeout   = 1500 * time.Millisecond
	maximumListedModelCount = 25
	autoPickModelCountLimit = 20
)

type savedConfig struct {
	EndpointURL string `json:"endpoint,omitempty"`
	ModelName   string `json:"model,omitempty"`
	APIKey      string `json:"apiKey,omitempty"`
}

func configFilePath() string {
	if explicit := strings.TrimSpace(os.Getenv("BLUECOLLAR_CONFIG")); explicit != "" {
		return explicit
	}
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return filepath.Join(configHome, "bluecollar", "config.json")
	}
	home, errorValue := os.UserHomeDir()
	if errorValue != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".config", "bluecollar", "config.json")
}

func loadSavedConfig(path string) savedConfig {
	if strings.TrimSpace(path) == "" {
		return savedConfig{}
	}
	data, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return savedConfig{}
	}
	var config savedConfig
	if json.Unmarshal(data, &config) != nil {
		return savedConfig{}
	}
	return config
}

func writeSavedConfig(path string, config savedConfig) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if errorValue := os.MkdirAll(filepath.Dir(path), 0o700); errorValue != nil {
		return errorValue
	}
	document, errorValue := json.MarshalIndent(config, "", "  ")
	if errorValue != nil {
		return errorValue
	}
	return os.WriteFile(path, append(document, '\n'), 0o600)
}

func geminiCLIConfigExists() bool {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		if _, errorValue := os.Stat(filepath.Join(configHome, "gemini-api", "config.yaml")); errorValue == nil {
			return true
		}
	}
	home, errorValue := os.UserHomeDir()
	if errorValue != nil || strings.TrimSpace(home) == "" {
		return false
	}
	_, statError := os.Stat(filepath.Join(home, ".config", "gemini-api", "config.yaml"))
	return statError == nil
}

func resolveConnectionDefaults(flagEndpoint string, flagAPIKey string, flagModel string, config savedConfig, getenv func(string) string) (string, string, string) {
	return resolveConnectionDefaultsWith(flagEndpoint, flagAPIKey, flagModel, config, getenv, geminiCLIBinaryAvailable(), geminiCLIConfigExists())
}

func resolveConnectionDefaultsWith(flagEndpoint string, flagAPIKey string, flagModel string, config savedConfig, getenv func(string) string, hasGeminiCLI bool, hasGeminiConfig bool) (string, string, string) {
	endpointURL := firstNonEmpty(flagEndpoint, getenv("BLUECOLLAR_MODEL_ENDPOINT"), config.EndpointURL)
	apiKey := firstNonEmpty(flagAPIKey, getenv("BLUECOLLAR_MODEL_API_KEY"), config.APIKey)
	modelName := firstNonEmpty(flagModel, getenv("BLUECOLLAR_MODEL"), config.ModelName)

	if endpointURL == "" {
		if openRouterKey := strings.TrimSpace(getenv("OPENROUTER_API_KEY")); openRouterKey != "" {
			endpointURL = "https://openrouter.ai/api/v1"
			apiKey = firstNonEmpty(apiKey, openRouterKey)
		} else if openAIKey := strings.TrimSpace(getenv("OPENAI_API_KEY")); openAIKey != "" {
			endpointURL = firstNonEmpty(getenv("OPENAI_BASE_URL"), "https://api.openai.com/v1")
			apiKey = firstNonEmpty(apiKey, openAIKey)
		} else if claudeToken := firstNonEmpty(getenv("CLAUDE_CODE_OAUTH_TOKEN"), getenv("ANTHROPIC_API_KEY")); claudeToken != "" {
			endpointURL = anthropicEndpointName
			apiKey = firstNonEmpty(apiKey, claudeToken)
		} else if geminiKey := strings.TrimSpace(getenv("GEMINI_API_KEY")); geminiKey != "" {
			if hasGeminiCLI {
				endpointURL = geminiCLIEndpointName
			} else {
				endpointURL = "https://generativelanguage.googleapis.com/v1beta/openai"
				apiKey = firstNonEmpty(apiKey, geminiKey)
			}
		} else if hasGeminiCLI && hasGeminiConfig {
			endpointURL = geminiCLIEndpointName
		} else {
			endpointURL = defaultLocalEndpointURL
		}
	}
	if isGeminiCLIEndpoint(endpointURL) {
		endpointURL = geminiCLIEndpointName
		if modelName == "" {
			modelName = defaultGeminiCLIModelName
		}
	}
	if isCodexEndpoint(endpointURL) {
		endpointURL = codexEndpointName
		if modelName == "" {
			modelName = defaultCodexModelName
		}
	}
	if isAnthropicEndpoint(endpointURL) {
		endpointURL = anthropicEndpointName
		apiKey = firstNonEmpty(apiKey, getenv("CLAUDE_CODE_OAUTH_TOKEN"), getenv("ANTHROPIC_API_KEY"))
		if modelName == "" {
			modelName = defaultAnthropicModelName
		}
	}
	return strings.TrimSuffix(strings.TrimSpace(endpointURL), "/"), strings.TrimSpace(apiKey), strings.TrimSpace(modelName)
}

func discoverEndpointModels(ctx context.Context, endpointURL string, apiKey string) ([]string, error) {
	if isGeminiCLIEndpoint(endpointURL) {
		return discoverGeminiCLIModels(ctx, apiKey)
	}
	if isCodexEndpoint(endpointURL) {
		return discoverCodexModels(ctx)
	}
	if isAnthropicEndpoint(endpointURL) {
		return discoverAnthropicModels(ctx, apiKey)
	}
	trimmedEndpoint := strings.TrimSuffix(strings.TrimSpace(endpointURL), "/")
	if trimmedEndpoint == "" {
		return nil, errors.New("no endpoint configured")
	}
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodGet, trimmedEndpoint+"/models", nil)
	if errorValue != nil {
		return nil, errorValue
	}
	if strings.TrimSpace(apiKey) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	client := &http.Client{Timeout: modelDiscoveryTimeout}
	response, errorValue := client.Do(request)
	if errorValue != nil {
		return nil, errorValue
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint returned %d for /models", response.StatusCode)
	}
	body, errorValue := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if errorValue != nil {
		return nil, errorValue
	}
	return parseModelListResponse(body)
}

func parseModelListResponse(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"models"`
	}
	if errorValue := json.Unmarshal(body, &payload); errorValue != nil {
		return nil, errorValue
	}
	seen := map[string]bool{}
	models := []string{}
	add := func(id string) {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || seen[trimmed] {
			return
		}
		seen[trimmed] = true
		models = append(models, trimmed)
	}
	for _, entry := range payload.Data {
		add(entry.ID)
	}
	for _, entry := range payload.Models {
		add(firstNonEmpty(entry.ID, entry.Name))
	}
	return models, nil
}

func autoSelectDiscoveredModel(ctx context.Context, endpointURL string, apiKey string) string {
	models, errorValue := discoverEndpointModels(ctx, endpointURL, apiKey)
	if errorValue != nil {
		return ""
	}
	return selectAutoModel(endpointURL, models)
}

func selectAutoModel(endpointURL string, models []string) string {
	if len(models) == 0 {
		return ""
	}
	if !isLocalEndpoint(endpointURL) && len(models) > autoPickModelCountLimit {
		return ""
	}
	for _, modelID := range models {
		if !isEmbeddingModelID(modelID) {
			return modelID
		}
	}
	return models[0]
}

func isLocalEndpoint(endpointURL string) bool {
	parsed, errorValue := url.Parse(strings.TrimSpace(endpointURL))
	if errorValue != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func isEmbeddingModelID(modelID string) bool {
	lower := strings.ToLower(modelID)
	return strings.Contains(lower, "embed") || strings.Contains(lower, "rerank")
}

func formatModelListForDisplay(endpointURL string, models []string) string {
	if len(models) == 0 {
		return "no models returned by " + endpointURL
	}
	sorted := append([]string{}, models...)
	sort.Strings(sorted)
	limit := min(len(sorted), maximumListedModelCount)
	lines := make([]string, 0, limit+2)
	lines = append(lines, fmt.Sprintf("models on %s (%d):", endpointURL, len(sorted)))
	for index := 0; index < limit; index++ {
		lines = append(lines, "  "+sorted[index])
	}
	if len(sorted) > limit {
		lines = append(lines, fmt.Sprintf("  … and %d more (use /model <name>)", len(sorted)-limit))
	}
	return strings.Join(lines, "\n")
}
