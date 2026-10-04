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
	"slices"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type compactionLiveCase struct {
	name            string
	prompt          string
	constraint      string
	records         []string
	wantedOmissions []string
}

var compactionLiveCases = []compactionLiveCase{
	{name: "expense report", prompt: "Prepare a September expense report for project Orion.", constraint: "Only include project Orion. Keep amounts in KRW and deliver CSV.", records: []string{"City weather forecast: sunny, 22 C.", "September project Orion expenses: stationery 42000 KRW; equipment 180000 KRW.", "September project Vega expenses: equipment 97000 KRW.", "Existing project Orion report is /workspace/reports/orion-september.csv."}, wantedOmissions: []string{"obs-001", "obs-003"}},
	{name: "korean expense report", prompt: "오리온 프로젝트의 9월 지출 보고서를 만들어줘.", constraint: "오리온 지출만 포함하고 금액은 원화로 유지해. CSV 파일로 줘.", records: []string{"오늘 서울 날씨: 맑음, 22도.", "9월 오리온 지출: 문구류 42000원, 장비 180000원.", "9월 베가 지출: 장비 97000원.", "기존 오리온 보고서 경로: /workspace/reports/orion-september.csv."}, wantedOmissions: []string{"obs-001", "obs-003"}},
	{name: "corrected destination", prompt: "Deliver the attendance spreadsheet to the destination I corrected.", constraint: "I originally said folder A, but changed it to folder B. Do not deliver to A.", records: []string{"Folder B ID is folder-b-7f9.", "The prepared spreadsheet is /workspace/attendance.xlsx.", "Unrelated recipe: flour 200 g and milk 100 ml.", "Unrelated image asset is /workspace/banner.png."}, wantedOmissions: []string{"obs-003", "obs-004"}},
	{name: "exact identifier", prompt: "Update the due date of the task titled Annual equipment inspection.", constraint: "Use the existing task; do not create a replacement task.", records: []string{"Exact matching task ID: task-73f6; title Annual equipment inspection.", "Current due date for task-73f6 is 2026-10-10.", "Unrelated task ID task-83e1 has title Team lunch.", "Unrelated lunch-menu lookup: soup and rice."}, wantedOmissions: []string{"obs-003", "obs-004"}},
	{name: "technical dependency", prompt: "Fix the CSV export bug and verify it.", constraint: "Keep the public API response shape unchanged.", records: []string{"The export handler is /workspace/src/export.ts; it produces CSV from rows returned by the API.", "The regression command is bun test tests/export.test.ts; failing assertion expects quoted commas.", "The API response shape is {rows: [{name: string, amount: number}]}; the export handler consumes rows.", "Unrelated theme color setting: purple."}, wantedOmissions: []string{"obs-004"}},
	{name: "assistant claim is not proof", prompt: "Check whether the requested report was actually delivered, and recover if it was not.", constraint: "The assistant said it sent the report. Confirm from tool evidence before saying it is done.", records: []string{"Delivery attempt for the requested report returned isError=true, HTTP 503; no attachment was stored.", "The requested report exists at /workspace/report.pdf.", "A different report from a different task was delivered yesterday.", "Unrelated weather lookup: cloudy."}, wantedOmissions: []string{"obs-003", "obs-004"}},
}

type recordedCompactionDecisionModel struct {
	model.DecisionModel
	request    model.DecisionRequest
	response   model.DecisionResponse
	errorValue error
}

type compactionEvidenceTransport struct {
	directory string
	sequence  int
}

func (transport *compactionEvidenceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.sequence++
	sequence := transport.sequence
	body, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return nil, errorValue
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	if errorValue := os.WriteFile(filepath.Join(transport.directory, fmt.Sprintf("wire-%02d-request.json", sequence)), body, 0600); errorValue != nil {
		return nil, errorValue
	}
	response, errorValue := http.DefaultTransport.RoundTrip(request)
	if errorValue != nil {
		return nil, errorValue
	}
	responseBody, readError := io.ReadAll(response.Body)
	response.Body.Close()
	if readError != nil {
		return nil, readError
	}
	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	if errorValue := os.WriteFile(filepath.Join(transport.directory, fmt.Sprintf("wire-%02d-response.json", sequence)), responseBody, 0600); errorValue != nil {
		return nil, errorValue
	}
	return response, nil
}

func (provider *recordedCompactionDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	provider.request = request
	provider.response, provider.errorValue = provider.DecisionModel.Decide(ctx, request)
	return provider.response, provider.errorValue
}

func TestLiveStructuredCompactionSelection(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}
	artifactDirectory := os.Getenv("BLUECOLLAR_COMPACTION_EVAL_ARTIFACTS")
	if artifactDirectory == "" {
		t.Fatal("BLUECOLLAR_COMPACTION_EVAL_ARTIFACTS is required")
	}
	if errorValue := os.MkdirAll(artifactDirectory, 0700); errorValue != nil {
		t.Fatal(errorValue)
	}
	provider := openaicompatible.NewProvider("https://openrouter.ai/api/v1", apiKey, "google/gemini-3.1-flash-lite")
	httpClient := &http.Client{Transport: &compactionEvidenceTransport{directory: artifactDirectory}, Timeout: 40 * time.Second}
	provider.UseHTTPClient(httpClient)
	decisionModel := &recordedCompactionDecisionModel{DecisionModel: decisions.Endpoint{URL: decisions.DefaultEndpointURL, ModelName: "typesafe/jev-1.13", APIKey: apiKey, HTTPClient: httpClient}.DecisionModel()}
	totalCost := 0.0
	maximumOutputTokens := 1200
	for _, testCase := range compactionLiveCases {
		if totalCost >= 0.10 {
			t.Fatalf("evaluation stopped at cost guard: $%.6f", totalCost)
		}
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
			defer cancel()
			state := compactionLiveState(testCase)
			summaryRequest := model.StructuredResponseRequest{Messages: []model.Message{{Role: "system", Content: taskContextSummaryInstruction()}, {Role: "user", Content: taskContextSummaryInput(state, TaskContextSummary{})}}, StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_task_context_summary", Document: taskContextSummarySchema(), IsStrictlyEnforced: true}, GenerationOptions: model.GenerationOptions{MaxTokens: &maximumOutputTokens}}
			startedAt := time.Now()
			response, summaryError := provider.GenerateStructuredResponse(ctx, summaryRequest)
			totalCost += response.Usage.CostUSD
			if summaryError != nil {
				writeCompactionLiveEvidence(t, artifactDirectory, testCase.name, map[string]any{"summaryRequest": summaryRequest, "summaryResponse": response, "error": summaryError.Error()})
				t.Fatal(summaryError)
			}
			content, errorValue := decodeTaskContextSummaryContent(response.Content)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			services := newTurnRunnerTestServices(provider, TurnOptions{})
			services.runner.UseDecisionModel(decisionModel)
			omissions := services.runner.irrelevantToolObservationIDs(ctx, "live-compaction", state, TaskContextSummary{TaskContextSummaryContent: content}, state.Observations)
			totalCost += decisionModel.response.Usage.CostUSD
			writeCompactionLiveEvidence(t, artifactDirectory, testCase.name, map[string]any{"summaryRequest": summaryRequest, "summaryResponse": response, "decisionRequest": decisionModel.request, "decisionResponse": decisionModel.response, "wantedOmissions": testCase.wantedOmissions, "actualOmissions": omissions, "latencyMilliseconds": time.Since(startedAt).Milliseconds(), "cumulativeReportedCostUSD": totalCost})
			if decisionModel.errorValue != nil {
				t.Fatal(decisionModel.errorValue)
			}
			if len(content.Constraints) == 0 {
				t.Error("summary omitted every user constraint")
			}
			if !slices.Equal(omissions, testCase.wantedOmissions) {
				t.Errorf("wanted omissions %v, got %v", testCase.wantedOmissions, omissions)
			}
			t.Logf("%s summary=%s decision=%s elapsed=%s cumulativeReportedCost=$%.6f", testCase.name, response.ModelName, decisionModel.response.ModelName, time.Since(startedAt).Round(time.Millisecond), totalCost)
		})
	}
}

func compactionLiveState(testCase compactionLiveCase) agentTaskState {
	state := agentTaskState{Request: AgentTurnRequest{Prompt: testCase.prompt, VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{{Speaker: "user", Text: testCase.constraint}}}}}
	for index, content := range testCase.records {
		state.Observations = append(state.Observations, turnObservation{ObservationID: nextObservationID(index + 1), Action: "continue", Tool: "file_read", ToolInput: json.RawMessage(`{"path":"sample.txt"}`), Output: toolcontract.ToolOutput{Content: content}, ToolIsReadOnly: true})
	}
	return state
}

func writeCompactionLiveEvidence(t *testing.T, directory string, name string, value any) {
	t.Helper()
	document, errorValue := json.MarshalIndent(value, "", "  ")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(directory, name+".json"), document, 0600); errorValue != nil {
		t.Fatal(errorValue)
	}
}
