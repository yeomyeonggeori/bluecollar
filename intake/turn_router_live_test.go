//go:build llmeval

package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestLiveTurnRouterRetainsTaskRecordContext(t *testing.T) {
	if os.Getenv("BLUECOLLAR_RECOVERY_LIVE") != "1" {
		t.Skip("set BLUECOLLAR_RECOVERY_LIVE=1 to exercise the live model")
	}
	provider, errorValue := (openaicompatible.Endpoint{
		URL:       os.Getenv("BLUECOLLAR_MODEL_ENDPOINT"),
		ModelName: os.Getenv("BLUECOLLAR_MODEL_NAME"),
		APIKey:    os.Getenv("BLUECOLLAR_MODEL_API_KEY"),
	}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	transport := &liveRouterTransport{}
	provider.UseHTTPClient(&http.Client{Transport: transport})
	taskTools := liveTaskRecordToolSet(t)
	router := NewTurnRouter(provider, agentcontract.IntakeOptions{IsEnabled: true})
	request := agentcontract.AgentRequest{
		Prompt:  "사업도 제대로 봐줘",
		ToolSet: taskTools,
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "사용자", Text: "업무 기록에 샘플 운영 점검을 추가해줘. 규모는 중간이고 유형은 운영으로 기록해."},
			{Speaker: "assistant", Text: "등록했습니다. task fields: title=샘플 운영 점검, size=medium, type=operations."},
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	decisions := []agentcontract.TurnDecision{}
	t.Cleanup(func() { writeLiveRouterEvidence(t, transport, decisions) })
	decision, errorValue := router.Plan(ctx, request)
	decisions = append(decisions, decision)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsAnyTool(decision.InitialToolNames, "task_list", "task_update") {
		t.Fatalf("expected the follow-up to retain task-record context, got route=%s classification=%s tools=%v", decision.Route, decision.Classification, decision.InitialToolNames)
	}
	if decision.Route == agentcontract.TurnRouteClarify || decision.Route == agentcontract.TurnRouteGiveUp {
		t.Fatalf("read available records and labels before requesting missing input: %+v", decision)
	}
	if containsTool(decision.InitialToolNames, "crm_read") {
		t.Fatalf("task business labels belong to the task record catalog: %+v", decision)
	}

	crmDecision, errorValue := router.Plan(ctx, agentcontract.AgentRequest{
		Prompt:  "CRM 고객 및 사업 기록을 읽어줘",
		ToolSet: taskTools,
	})
	decisions = append(decisions, crmDecision)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if !containsTool(crmDecision.InitialToolNames, "crm_read") {
		t.Fatalf("expected a self-contained business read to use the CRM tool, got route=%s classification=%s tools=%v", crmDecision.Route, crmDecision.Classification, crmDecision.InitialToolNames)
	}
}

func TestLiveIntakeFailureNoticeUsesRecordedFacts(t *testing.T) {
	if os.Getenv("BLUECOLLAR_RECOVERY_LIVE") != "1" {
		t.Skip("set BLUECOLLAR_RECOVERY_LIVE=1 to exercise the live model")
	}
	provider, errorValue := (openaicompatible.Endpoint{
		URL:       os.Getenv("BLUECOLLAR_MODEL_ENDPOINT"),
		ModelName: os.Getenv("BLUECOLLAR_MODEL_NAME"),
		APIKey:    os.Getenv("BLUECOLLAR_MODEL_API_KEY"),
	}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	transport := &liveRouterTransport{}
	provider.UseHTTPClient(&http.Client{Transport: transport})
	report := agentcontract.BuildIntakeFailureReport(agentcontract.IntakeFailureReportInput{
		Classification:            agentcontract.IntakeClassificationBoundedTask,
		OriginalRequest:           "업무의 사업 분류도 제대로 봐줘",
		PlannedInterpretation:     "Read business deal records",
		UnverifiedUserFacingReply: "사업 딜 현황을 조회하겠습니다.",
		ResponseLanguage:          "ko",
		DiagnosticEventID:         "live-intake-failure-sample",
		MaxElapsedSecond:          172,
		ElapsedSecond:             115,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	notice, status := (agentcontract.FailureNoticeGenerator{LanguageModel: provider}).Generate(ctx, report)
	writeLiveFailureNoticeEvidence(t, transport, notice)
	if status.Source == "raw_error" {
		t.Fatalf("expected model-generated notice: %+v", status)
	}
	if strings.TrimSpace(notice.Message) == "" {
		t.Fatal("expected a nonempty generated Korean intake failure notice")
	}
}

func liveTaskRecordToolSet(t *testing.T) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"task_list", "task_update", "crm_read"})
	toolSet.AllowTestReplacement()
	for _, toolDefinition := range []toolcontract.ToolDefinition{
		{Name: "task_list", Description: "List workspace tasks with optional filters. It reads the requester's own tasks unless other people are named.", OutputSchema: []byte(`{"type":"object","properties":{"tasks":{"type":"array","items":{"type":"object"}},"registeredLabels":{"type":"object"}},"required":["tasks","registeredLabels"],"additionalProperties":false}`)},
		{Name: "task_update", Description: "Update explicit fields on an existing task. taskHint is the exact task ID or current title from a task_list result.", InputSchema: []byte(`{"type":"object","properties":{"taskHint":{"type":"string"},"business":{"type":"string"},"type":{"type":"string"},"size":{"type":"string"}},"required":["taskHint"],"additionalProperties":false}`)},
		{Name: "crm_read", Description: "Read business and customer records from the CRM."},
	} {
		toolDefinition = liveCatalogDefinition(t, toolDefinition)
		toolDefinition.ID = "live-test:" + toolDefinition.Name
		toolDefinition.Visibility = toolcontract.ToolVisibilityModel
		if len(toolDefinition.InputSchema) == 0 {
			toolDefinition.InputSchema = []byte(`{"type":"object","properties":{},"additionalProperties":false}`)
		}
		if errorValue := registerTestTool(toolSet, toolDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			return testToolSuccess("ok"), nil
		}); errorValue != nil {
			t.Fatal(errorValue)
		}
	}
	return toolSet
}

func liveCatalogDefinition(t *testing.T, fallback toolcontract.ToolDefinition) toolcontract.ToolDefinition {
	t.Helper()
	path := os.Getenv("BLUECOLLAR_ROUTER_CATALOG")
	if path == "" || fallback.Name == "crm_read" {
		return fallback
	}
	document, errorValue := os.ReadFile(path)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var catalog struct {
		Tools []struct {
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	for _, definition := range catalog.Tools {
		if definition.Name == fallback.Name {
			fallback.Description = definition.Description
			fallback.InputSchema = definition.InputSchema
			fallback.OutputSchema = definition.OutputSchema
			return fallback
		}
	}
	t.Fatalf("catalog lacks %s", fallback.Name)
	return fallback
}

type liveRouterTransport struct {
	requests  []json.RawMessage
	responses []json.RawMessage
	elapsed   []time.Duration
}

func (transport *liveRouterTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	document, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return nil, errorValue
	}
	request.Body = io.NopCloser(bytes.NewReader(document))
	transport.requests = append(transport.requests, append(json.RawMessage{}, document...))
	started := time.Now()
	response, errorValue := http.DefaultTransport.RoundTrip(request)
	transport.elapsed = append(transport.elapsed, time.Since(started))
	if errorValue != nil {
		return nil, errorValue
	}
	document, errorValue = io.ReadAll(response.Body)
	response.Body.Close()
	if errorValue != nil {
		return nil, errorValue
	}
	response.Body = io.NopCloser(bytes.NewReader(document))
	if json.Valid(document) {
		transport.responses = append(transport.responses, append(json.RawMessage{}, document...))
	}
	return response, nil
}

func writeLiveRouterEvidence(t *testing.T, transport *liveRouterTransport, decisions []agentcontract.TurnDecision) {
	t.Helper()
	directory := os.Getenv("BLUECOLLAR_RECOVERY_EVIDENCE")
	if directory == "" {
		directory = t.TempDir()
	}
	if errorValue := os.MkdirAll(directory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	evidence, errorValue := json.MarshalIndent(map[string]any{
		"requests":           transport.requests,
		"responses":          transport.responses,
		"elapsedNanoseconds": transport.elapsed,
		"decisions":          decisions,
	}, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(directory, "turn-router-context.json"), evidence, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func writeLiveFailureNoticeEvidence(t *testing.T, transport *liveRouterTransport, notice agentcontract.FailureNotice) {
	t.Helper()
	directory := os.Getenv("BLUECOLLAR_RECOVERY_EVIDENCE")
	if directory == "" {
		directory = t.TempDir()
	}
	if errorValue := os.MkdirAll(directory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	evidence, errorValue := json.MarshalIndent(map[string]any{
		"requests":           transport.requests,
		"responses":          transport.responses,
		"elapsedNanoseconds": transport.elapsed,
		"notice":             notice,
	}, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(directory, "intake-failure-notice.json"), evidence, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func containsAnyTool(toolNames []string, candidates ...string) bool {
	for _, candidate := range candidates {
		if containsTool(toolNames, candidate) {
			return true
		}
	}
	return false
}

func containsTool(toolNames []string, candidate string) bool {
	for _, toolName := range toolNames {
		if strings.TrimSpace(toolName) == candidate {
			return true
		}
	}
	return false
}
