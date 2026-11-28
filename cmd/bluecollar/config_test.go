package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConnectionDefaultsPriority(t *testing.T) {
	saved := savedConfig{
		EndpointURL: "http://saved.local/v1",
		ModelName:   "saved-model",
		APIKey:      "saved-key",
	}
	emptyEnv := func(string) string { return "" }

	endpoint, apiKey, model := resolveConnectionDefaults("", "", "", saved, emptyEnv)
	if endpoint != "http://saved.local/v1" || apiKey != "saved-key" || model != "saved-model" {
		t.Fatalf("expected saved config defaults, got %q %q %q", endpoint, apiKey, model)
	}

	openRouterEnv := func(key string) string {
		if key == "OPENROUTER_API_KEY" {
			return "or-key"
		}
		return ""
	}
	endpoint, apiKey, model = resolveConnectionDefaults("", "", "", savedConfig{}, openRouterEnv)
	if endpoint != "https://openrouter.ai/api/v1" || apiKey != "or-key" || model != "" {
		t.Fatalf("expected OpenRouter env fallback, got %q %q %q", endpoint, apiKey, model)
	}

	endpoint, apiKey, model = resolveConnectionDefaults("", "", "", savedConfig{}, emptyEnv)
	if endpoint != defaultLocalEndpointURL || apiKey != "" || model != "" {
		t.Fatalf("expected local Ollama fallback, got %q %q %q", endpoint, apiKey, model)
	}
}

func TestAutoSelectDiscoveredModelSkipsEmbeddingModels(t *testing.T) {
	models, errorValue := parseModelListResponse([]byte(`{"data":[{"id":"nomic-embed-text:latest"},{"id":"qwen3.5:4b"}]}`))
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	selected := selectAutoModel(defaultLocalEndpointURL, models)
	if selected != "qwen3.5:4b" {
		t.Fatalf("expected first non-embedding model, got %q", selected)
	}
}

func TestSwitchingModelAndEndpointPersistsConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "bluecollar", "config.json")
	session := &conversationSession{
		options: runOptions{
			endpointURL: defaultLocalEndpointURL,
			configPath:  configPath,
		},
	}

	output := &bytes.Buffer{}
	handleReplCommand(context.Background(), session, "/endpoint http://127.0.0.1:8080/v1", output)
	handleReplCommand(context.Background(), session, "/model qwen3.5:4b", output)

	loaded := loadSavedConfig(configPath)
	if loaded.EndpointURL != "http://127.0.0.1:8080/v1" || loaded.ModelName != "qwen3.5:4b" {
		t.Fatalf("expected saved config to persist endpoint and model, got %+v", loaded)
	}
}

func TestTUIBareSetupStateWithoutModelRendersGuidance(t *testing.T) {
	frame := renderTUISnapshot(tuiSnapshot{
		width:         96,
		height:        26,
		endpointURL:   defaultLocalEndpointURL,
		workspacePath: "/tmp/work",
		editor:        newComposerEditor(),
	})
	joined := stripANSI(strings.Join(frame.plainLines, "\n"))
	for _, expected := range []string{"● setup", "not set (run /models)", defaultLocalEndpointURL, "Run /models to discover models"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected bare setup frame to include %q, got:\n%s", expected, joined)
		}
	}
}
