//go:build llmeval

package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recoveryRecordedTransport struct {
	requests  []json.RawMessage
	responses []json.RawMessage
	elapsed   []time.Duration
}

func (transport *recoveryRecordedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	document, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return nil, errorValue
	}
	request.Body = io.NopCloser(bytes.NewReader(document))
	transport.requests = append(transport.requests, json.RawMessage(document))
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
		transport.responses = append(transport.responses, json.RawMessage(document))
	}
	return response, nil
}

func TestLiveRecoveryJudgment(t *testing.T) {
	if os.Getenv("BLUECOLLAR_RECOVERY_LIVE") != "1" {
		t.Skip("set BLUECOLLAR_RECOVERY_LIVE=1 to exercise the live model")
	}
	for _, isRepairable := range []bool{false, true} {
		t.Run(fmt.Sprintf("repairable_%t", isRepairable), func(t *testing.T) {
			runLiveRecoveryJudgment(t, isRepairable)
		})
	}
}

func runLiveRecoveryJudgment(t *testing.T, isRepairable bool) {
	t.Helper()
	provider, errorValue := (openaicompatible.Endpoint{URL: os.Getenv("BLUECOLLAR_MODEL_ENDPOINT"), ModelName: os.Getenv("BLUECOLLAR_MODEL_NAME"), APIKey: os.Getenv("BLUECOLLAR_MODEL_API_KEY")}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	transport := &recoveryRecordedTransport{}
	provider.UseHTTPClient(&http.Client{Transport: transport})
	services := newTurnRunnerTestServices(provider, TurnOptions{MaxIterationCount: 8, MaxToolCallCount: 4, MaxElapsedSecond: 300})
	tools := newTestCapabilityToolSet([]string{"record_create"})
	inputs := []json.RawMessage{}
	registerTestTool(tools, toolcontract.ToolDefinition{
		Name: "record_create", Description: "Create a record in the requester's work list. Keep the user-specified title. warehouseID selects the storage location when required by the server.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"warehouseID":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
	}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		inputs = append(inputs, append(json.RawMessage{}, invocation.Input...))
		if !isRepairable {
			return toolcontract.ToolFailureResult(toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "result_validation", "The record server returned a response incompatible with its result contract. The operation may already have been saved; the server requires a software update. This tool cannot update the server."), nil
		}
		var input struct {
			WarehouseID string `json:"warehouseID"`
		}
		if errorValue := json.Unmarshal(invocation.Input, &input); errorValue != nil {
			return toolcontract.ToolResult{}, errorValue
		}
		if input.WarehouseID == "warehouse-7" {
			return testToolSuccess(`{"recordID":"record-1"}`), nil
		}
		result := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "input_validation", "warehouseID is required. The requester's only authorized warehouseID is warehouse-7.")
		result.Failure.Retryable = true
		result.Failure.SafeRetry = true
		return result, nil
	})
	request := AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1", Prompt: "'샘플 제품소개서'를 내 업무 목록에 등록해 줘.", ToolSet: tools, PinnedToolNames: tools.ListToolNames(), RequiredEvidenceTools: []string{"record_create"}, OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"record_create"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result, runError := services.runner.RunTurn(ctx, request)
	evidence := map[string]any{"requests": transport.requests, "responses": transport.responses, "elapsedNanoseconds": transport.elapsed, "inputs": inputs, "result": result, "events": services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)}
	document, errorValue := json.MarshalIndent(evidence, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	directory := os.Getenv("BLUECOLLAR_RECOVERY_EVIDENCE")
	if directory == "" {
		directory = t.TempDir()
	}
	if errorValue := os.MkdirAll(directory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(directory, fmt.Sprintf("repairable-%t.json", isRepairable)), document, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if runError != nil {
		t.Fatal(runError)
	}
	expectedStatus, expectedCalls := agentcontract.TaskStatusFailed, 1
	if isRepairable {
		expectedStatus, expectedCalls = agentcontract.TaskStatusCompleted, 2
	}
	if result.TaskRun.Status != expectedStatus || len(inputs) != expectedCalls {
		t.Fatalf("status=%s calls=%d; expected status=%s calls=%d; reason=%s", result.TaskRun.Status, len(inputs), expectedStatus, expectedCalls, result.TaskRun.FailureReason)
	}
	if !isRepairable {
		if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.action", `"action":"fail"`) {
			t.Fatal("failure must be an explicit model decision, not a provider or action-parsing failure")
		}
		if len(transport.requests) != 2 {
			t.Fatalf("an unrecoverable tool failure spent %d model requests", len(transport.requests))
		}
	}
}
