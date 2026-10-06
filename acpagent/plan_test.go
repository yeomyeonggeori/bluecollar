package acpagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/llmcalls"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type routingStateLanguageModel struct {
	*scriptedLanguageModel
	mutex          sync.Mutex
	routingStates  []string
	routingSchemas int
}

func (languageModel *routingStateLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == llmcalls.TurnRouterSchemaName {
		languageModel.mutex.Lock()
		languageModel.routingSchemas++
		languageModel.routingStates = append(languageModel.routingStates, allMessageContent(request))
		languageModel.mutex.Unlock()
	}
	return languageModel.scriptedLanguageModel.GenerateStructuredResponse(ctx, request)
}

func (languageModel *routingStateLanguageModel) routingCallCount() int {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return languageModel.routingSchemas
}

func (languageModel *routingStateLanguageModel) routingStateText() string {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	return strings.Join(languageModel.routingStates, "\n")
}

func finishedRunLedger(t *testing.T, hostCalls *[]hostToolCall) []agentcontract.LedgerRecord {
	t.Helper()
	firstHost := &hostClient{}
	firstSession := openPipedHost(t, testOptions(noteWriteTurnScript()), publishedCatalogTransport(t, hostCalls), firstHost)
	if _, errorValue := firstSession.prompt(t, map[string]any{TaskRunMetaKey: "host-run-7"}, acp.TextBlock("회의록 정리해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}
	return firstHost.keptLedger()
}

func turnResultOf(t *testing.T, promptResponse acp.PromptResponse) agentcontract.AgentTurnResult {
	t.Helper()
	encoded, errorValue := json.Marshal(promptResponse.Meta[TurnResultMetaKey])
	if errorValue != nil {
		t.Fatalf("the turn result has to travel as JSON: %v", errorValue)
	}
	turnResult := agentcontract.AgentTurnResult{}
	if errorValue := json.Unmarshal(encoded, &turnResult); errorValue != nil {
		t.Fatalf("the turn result has to read back as the contract type: %v", errorValue)
	}
	return turnResult
}

func replyingScript() *scriptedLanguageModel {
	return &scriptedLanguageModel{contents: []string{
		`{"action":"reply","final":true,"message":"이어서 마쳤습니다","goalSatisfied":true,"completionEvidenceIDs":["obs-001"]}`,
	}}
}

func TestARunTheHostResumesIsContinuedWithoutPlanningItAgain(t *testing.T) {
	hostCalls := []hostToolCall{}
	ledger := finishedRunLedger(t, &hostCalls)
	languageModel := &routingStateLanguageModel{scriptedLanguageModel: replyingScript()}
	handedOver := agentcontract.AgentTurnRequest{IsRuntimeRestartResume: true, ExistingTaskRunID: "host-run-7", ResponseLanguage: "ko"}

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), &hostClient{})
	promptResponse, errorValue := host.prompt(t, map[string]any{TaskRunMetaKey: "host-run-7", agentcontract.LedgerMetaKey: ledger, TurnRequestMetaKey: handedOver}, acp.TextBlock("회의록 정리해줘"))
	if errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if languageModel.routingCallCount() != 0 {
		t.Fatalf("a run the host resumes has nothing new to plan, got %d routing calls", languageModel.routingCallCount())
	}
	if route := turnResultOf(t, promptResponse).TurnRoute; route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("a resumed run continues, got route %q", route)
	}
}

func TestAReplyToTheQuestionARunAskedIsContinuedWithoutPlanningItAgain(t *testing.T) {
	hostCalls := []hostToolCall{}
	ledger := finishedRunLedger(t, &hostCalls)
	languageModel := &routingStateLanguageModel{scriptedLanguageModel: replyingScript()}
	handedOver := agentcontract.AgentTurnRequest{
		ExistingTaskRunID: "host-run-7",
		PendingInput:      agentcontract.PendingInputContext{TaskRunID: "host-run-7", Question: "어느 회의록입니까?"},
	}

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), &hostClient{})
	promptResponse, errorValue := host.prompt(t, map[string]any{TaskRunMetaKey: "host-run-7", agentcontract.LedgerMetaKey: ledger, TurnRequestMetaKey: handedOver}, acp.TextBlock("어제 회의록이요"))
	if errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if languageModel.routingCallCount() != 0 {
		t.Fatalf("an answer to the run's own question is not a new request to plan, got %d routing calls", languageModel.routingCallCount())
	}
	if route := turnResultOf(t, promptResponse).TurnRoute; route != agentcontract.TurnRouteContinueTask {
		t.Fatalf("an answered question continues its run, got route %q", route)
	}
}

func TestTheFactsAHostHandsOverReachThePlanner(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &routingStateLanguageModel{scriptedLanguageModel: replyingScript()}
	handedOver := agentcontract.AgentTurnRequest{
		ActiveGoal:   agentcontract.ActiveGoal{OriginalInstruction: "분기 매출표를 만들어줘", CurrentObjective: "분기 매출표를 만들어줘", Status: agentcontract.ActiveGoalStatusBlocked},
		PriorTask:    agentcontract.PriorTaskContext{Prompt: "지난달 보고서 정리", Result: "정리 끝"},
		ScheduledRun: agentcontract.ScheduledRunContext{ScheduleID: "schedule-1", Kind: "cron", Name: "주간 리마인더"},
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "박예시", Text: "표 양식은 지난번 그대로요"},
		}},
	}

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, map[string]any{TurnRequestMetaKey: handedOver}, acp.TextBlock("이어서 해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	routingState := languageModel.routingStateText()
	for _, fact := range []string{"분기 매출표를 만들어줘", "지난달 보고서 정리", "주간 리마인더", "표 양식은 지난번 그대로요"} {
		if !strings.Contains(routingState, fact) {
			t.Fatalf("the planner is never shown %q, so the agent plans without a fact the host handed over:\n%s", fact, routingState)
		}
	}
}

func TestATaskLevelTheHostPinsIsTheLevelTheTurnRunsAt(t *testing.T) {
	hostCalls := []hostToolCall{}
	languageModel := &scriptedLanguageModel{level: "high", contents: replyingScript().contents}
	handedOver := agentcontract.AgentTurnRequest{TaskLevel: agentcontract.TaskLevelXLow}
	client := &hostClient{}

	host := openPipedHost(t, testOptions(languageModel), publishedCatalogTransport(t, &hostCalls), client)
	if _, errorValue := host.prompt(t, map[string]any{TurnRequestMetaKey: handedOver}, acp.TextBlock("한 줄로 답해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}

	if level := recordedIntakeLevel(t, client); level != string(agentcontract.TaskLevelXLow) {
		t.Fatalf("the host pinned the level to %s, so the planner's own answer of high may not decide it, got %q", agentcontract.TaskLevelXLow, level)
	}
}

func recordedIntakeLevel(t *testing.T, client *hostClient) string {
	t.Helper()
	for _, record := range client.keptLedger() {
		if record.Name != agentcontract.TaskEventAgentIntake {
			continue
		}
		var intake struct {
			Level string `json:"level"`
		}
		if errorValue := json.Unmarshal(record.Body, &intake); errorValue != nil {
			t.Fatalf("the intake record has to read as JSON: %v", errorValue)
		}
		return intake.Level
	}
	t.Fatal("the turn recorded no intake decision")
	return ""
}

func recordedCallCount(t *testing.T, handedOver agentcontract.AgentTurnRequest) int {
	t.Helper()
	hostCalls := []hostToolCall{}
	repository := &recordingLLMCallRepository{}
	options := testOptions(wireRecordingLanguageModel{replyingScript()})
	options.LLMCallRepository = repository

	host := openPipedHost(t, options, publishedCatalogTransport(t, &hostCalls), &hostClient{})
	if _, errorValue := host.prompt(t, map[string]any{TaskRunMetaKey: "host-run-9", TurnRequestMetaKey: handedOver}, acp.TextBlock("한 줄로 답해줘")); errorValue != nil {
		t.Fatalf("session/prompt: %v", errorValue)
	}
	for _, call := range repository.recorded() {
		if call.taskRunID != "host-run-9" {
			t.Fatalf("a call recorded under %q, not the run the host named", call.taskRunID)
		}
	}
	return len(repository.recorded())
}

func TestThePlanningCallsAreRecordedUnderTheRunTheyPlanned(t *testing.T) {
	withoutPlanning := recordedCallCount(t, agentcontract.AgentTurnRequest{IsRuntimeRestartResume: true})
	withPlanning := recordedCallCount(t, agentcontract.AgentTurnRequest{})

	if withPlanning <= withoutPlanning {
		t.Fatalf("planning a turn is a model call the run has to show, got %d calls with it and %d without", withPlanning, withoutPlanning)
	}
}

func TestAResumedRunInheritsTheHighestLevelItRecorded(t *testing.T) {
	taskEvents := []agentcontract.TaskEvent{
		{Name: agentcontract.TaskEventAgentIntake, Body: `{"effortLevel":"deep","taskComplexity":"complex"}`},
		{Name: agentcontract.TaskEventAgentIntake, Body: `{"effortLevel":"standard","taskComplexity":"normal"}`},
	}

	decision, isFromFacts := DecisionFromFacts(taskEvents, agentcontract.AgentTurnRequest{IsRuntimeRestartResume: true})

	if !isFromFacts || decision.TaskLevel != agentcontract.TaskLevelMedium {
		t.Fatalf("a resume carries the highest recorded level, got %q from facts %v", decision.TaskLevel, isFromFacts)
	}
}

func TestAResumedRunWithNoRecordedLevelRunsAtLow(t *testing.T) {
	taskEvents := []agentcontract.TaskEvent{{Name: agentcontract.TaskEventAgentIntake, Body: "not-json"}}

	decision, _ := DecisionFromFacts(taskEvents, agentcontract.AgentTurnRequest{IsRuntimeRestartResume: true})

	if decision.TaskLevel != agentcontract.TaskLevelLow {
		t.Fatalf("a run with no recorded level resumes at low, got %q", decision.TaskLevel)
	}
}

func TestAReplyToAskedQuestionRestoresNothingFromTheRunsIntake(t *testing.T) {
	taskEvents := []agentcontract.TaskEvent{{Name: agentcontract.TaskEventAgentIntake, Body: `{"taskLevel":"high","taskShape":"research_task","classification":"bounded_task"}`}}
	request := agentcontract.AgentTurnRequest{PendingInput: agentcontract.PendingInputContext{TaskRunID: "run-1"}}

	decision, isFromFacts := DecisionFromFacts(taskEvents, request)

	if !isFromFacts || decision.TaskLevel != "" || decision.TaskShape != agentcontract.TaskShapeMaintenanceTask {
		t.Fatalf("an answer continues the run bare, got %+v from facts %v", decision, isFromFacts)
	}
}

func TestAFreshTurnHasNoDecisionInItsFacts(t *testing.T) {
	if _, isFromFacts := DecisionFromFacts(nil, agentcontract.AgentTurnRequest{Prompt: "회의록 정리해줘"}); isFromFacts {
		t.Fatal("a fresh turn is planned, not read from its facts")
	}
}
