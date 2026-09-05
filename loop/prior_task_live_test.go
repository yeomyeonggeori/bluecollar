package loop

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestLivePriorTaskReinterpretation(t *testing.T) {
	if os.Getenv("BLUECOLLAR_RECOVERY_LIVE") != "1" {
		t.Skip("set BLUECOLLAR_RECOVERY_LIVE=1 to exercise the live model")
	}
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
		Name: "record_create", Description: "Create work for the requester. title is the exact requested title. participantPersonHints names additional human participants explicitly requested by the user; omit it when none are requested. Dates are optional.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"participantPersonHints":{"type":"array","items":{"type":"string"}}},"required":["title"],"additionalProperties":false}`),
	}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		inputs = append(inputs, append(json.RawMessage{}, invocation.Input...))
		return testToolSuccess(`{"recordID":"record-1"}`), nil
	})
	priorFailure := toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.NotFound, "target_resolution", "No participant matches 샘플봇")
	request := AgentTurnRequest{
		RequesterPersonID: "person-1", ConversationID: "conversation-1", Prompt: "다시 해봐. 제목은 앞서 정정한 그대로 해 줘.",
		AgentIdentity: AgentIdentity{Name: "샘플봇", Handle: "samplebot"}, ToolSet: tools, PinnedToolNames: tools.ListToolNames(),
		OutcomeContract: OutcomeContract{RequiredEvidenceTools: []string{"record_create"}},
		PriorTask: PriorTaskContext{
			TaskRunID: "previous", Status: "failed", Prompt: "샘플봇, '하드웨어 기획서' 예정 업무 추가. 정정: 제목을 '샘플봇 제품소개서'로 바꿔서 등록해 줘.",
			Result:           "샘플봇을 참여자로 찾을 수 없어 등록하지 못했습니다. 날짜도 필요합니다.",
			RecordedAttempts: []agentcontract.PriorTaskAttempt{{ObservationID: "old-attempt", Tool: "record_create", ToolInput: json.RawMessage(`{"title":"제품소개서","participantPersonHints":["샘플봇"]}`), Failure: priorFailure.Failure}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result, runError := services.runner.RunTurn(ctx, request)
	document, errorValue := json.MarshalIndent(map[string]any{"requests": transport.requests, "responses": transport.responses, "elapsedNanoseconds": transport.elapsed, "inputs": inputs, "result": result, "events": services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)}, "", "  ")
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
	if errorValue := os.WriteFile(filepath.Join(directory, "prior-task.json"), document, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	if runError != nil || result.TaskRun.Status != agentcontract.TaskStatusCompleted || len(inputs) != 1 {
		t.Fatalf("status=%s inputs=%s error=%v", result.TaskRun.Status, inputs, runError)
	}
	var input struct {
		Title        string   `json:"title"`
		Participants []string `json:"participantPersonHints"`
	}
	if errorValue := json.Unmarshal(inputs[0], &input); errorValue != nil {
		t.Fatal(errorValue)
	}
	if input.Title != "샘플봇 제품소개서" || len(input.Participants) != 0 {
		t.Fatalf("replayed the previous misinterpretation: %s", inputs[0])
	}
}
