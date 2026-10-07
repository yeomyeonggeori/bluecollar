package approval

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

type fixture struct {
	gate    *Gate
	store   *taskstate.TaskRunService
	taskRun agentcontract.TaskRun
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	return newFixtureWith(t, nil, &scriptedAsker{})
}

func newFixtureWith(t *testing.T, languageModel model.LanguageModelProvider, asker Asker) fixture {
	t.Helper()
	store := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := store.CreateTaskRun("person-1", "conversation-1", "내일 회의 지워줘")
	return fixture{gate: New(store, languageModel, asker), store: store, taskRun: taskRun}
}

func (fixture fixture) request() approvalRequest {
	return requestFixture(fixture.taskRun.TaskRunID)
}

func (fixture fixture) scopedRequest() approvalRequest {
	request := fixture.request()
	request.toolDefinition.ApprovalScope = "calendar"
	return request
}

func (fixture fixture) requestWithInput(toolInput string) approvalRequest {
	request := fixture.request()
	request.toolInput = json.RawMessage(toolInput)
	return request
}

func (fixture fixture) awaitOutcome(request approvalRequest) approvalcore.Outcome {
	return fixture.gate.awaitApproval(context.Background(), request)
}

func (fixture fixture) events() []agentcontract.TaskEvent {
	return fixture.store.ListTaskEvent(fixture.taskRun.TaskRunID)
}

func (fixture fixture) record(eventName string, body string) {
	fixture.store.AppendTaskEvent(fixture.taskRun.TaskRunID, eventName, body)
}

func (fixture fixture) pendingHold(t *testing.T) holdrecord.Hold {
	t.Helper()
	holds := holdrecord.Holds(fixture.events())
	if len(holds) == 0 {
		t.Fatalf("expected a hold, got %v", fixture.eventNames())
	}
	hold := holds[len(holds)-1]
	if hold.State != holdrecord.StatePending {
		t.Fatalf("expected a hold waiting for an answer, got %v", fixture.eventNames())
	}
	return hold
}

func (fixture fixture) answer(t *testing.T, answer approvalcore.Verdict, source string) approvalcore.Verdict {
	t.Helper()
	return fixture.gate.settle(fixture.taskRun.TaskRunID, fixture.pendingHold(t), answer, source)
}

func (fixture fixture) taskStatus() agentcontract.TaskStatus {
	taskRun, _ := fixture.store.FindTaskRun(fixture.taskRun.TaskRunID)
	return taskRun.Status
}

func (fixture fixture) eventBody(t *testing.T, eventName string) string {
	t.Helper()
	for _, taskEvent := range fixture.events() {
		if taskEvent.Name == eventName {
			return taskEvent.Body
		}
	}
	t.Fatalf("expected a %s event to be recorded, got %v", eventName, fixture.eventNames())
	return ""
}

func (fixture fixture) eventNamed(eventName string) agentcontract.TaskEvent {
	for _, taskEvent := range fixture.events() {
		if taskEvent.Name == eventName {
			return taskEvent
		}
	}
	return agentcontract.TaskEvent{}
}

func (fixture fixture) eventNames() []string {
	names := []string{}
	for _, taskEvent := range fixture.events() {
		names = append(names, taskEvent.Name)
	}
	return names
}

func (fixture fixture) hasEvent(eventName string) bool {
	for _, name := range fixture.eventNames() {
		if name == eventName {
			return true
		}
	}
	return false
}

func requestFixture(taskRunID string) approvalRequest {
	return approvalRequest{
		taskRunID:      taskRunID,
		toolDefinition: toolcontract.ToolDefinition{Name: "event_delete", RequiresApproval: true},
		toolInput:      json.RawMessage(`{"eventID":"event-1"}`),
	}
}

type wordingLanguageModel struct {
	question    string
	failure     error
	lastRequest model.StructuredResponseRequest
}

func (languageModel *wordingLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "", errors.New("the approval gate only asks for structured output")
}

func (languageModel *wordingLanguageModel) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	languageModel.lastRequest = request
	if languageModel.failure != nil {
		return model.StructuredResponse{}, languageModel.failure
	}
	return model.StructuredResponse{Content: `{"question":"` + languageModel.question + `"}`}, nil
}

func (languageModel *wordingLanguageModel) promptSeen() string {
	messages := []string{}
	for _, message := range languageModel.lastRequest.Messages {
		messages = append(messages, message.Content)
	}
	return strings.Join(messages, "\n")
}

type scriptedAsker struct {
	answer          approvalcore.Verdict
	askedCount      int
	holds           []holdrecord.Hold
	beforeAnswering func()
}

func (asker *scriptedAsker) Ask(_ context.Context, hold holdrecord.Hold) approvalcore.Verdict {
	asker.askedCount++
	asker.holds = append(asker.holds, hold)
	if asker.beforeAnswering != nil {
		asker.beforeAnswering()
	}
	return asker.answer
}

func approvalToolSet(t *testing.T, executed *[]string) *toolcontract.ToolSet {
	t.Helper()
	toolSet := toolcontract.NewToolSet([]string{"file_delete", "file_read"})
	toolSet.AllowTestReplacement()
	register := func(name string, requiresApproval bool, approvalScope string) {
		errorValue := toolSet.RegisterTool(toolcontract.ToolDefinition{
			ID:               "test:" + name,
			Name:             name,
			Description:      name,
			Visibility:       toolcontract.ToolVisibilityModel,
			InputSchema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			RequiresApproval: requiresApproval,
			ApprovalScope:    approvalScope,
			SideEffectClass:  toolcontract.ToolSideEffectStateChange,
			ResultContract:   &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object"}`)},
		}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
			*executed = append(*executed, invocation.ToolName)
			return toolcontract.ToolSuccessData("done", json.RawMessage(`{}`)), nil
		})
		if errorValue != nil {
			t.Fatalf("expected %s to register: %v", name, errorValue)
		}
	}
	register("file_delete", true, "workspace_files")
	register("file_read", false, "")
	return toolSet
}

func invokeThroughGate(t *testing.T, invocationContext context.Context, toolCallGate toolcontract.ToolCallGate, toolName string) (*[]string, toolcontract.ToolResult) {
	t.Helper()
	executed := []string{}
	toolSet := approvalToolSet(t, &executed)
	toolSet.UseToolCallGate(toolCallGate)
	result, errorValue := toolSet.Invoke(invocationContext, toolcontract.ToolInvocation{
		ToolName: toolName,
		Input:    json.RawMessage(`{"path":"~/notes.md"}`),
	})
	if errorValue != nil {
		t.Fatalf("expected the call to reach the tool set: %v", errorValue)
	}
	return &executed, result
}

var errLanguageModelUnreachable = errors.New("the language model is unreachable")

func withTaskRun(fixture fixture) context.Context {
	return toolcontract.WithTaskRunID(context.Background(), fixture.taskRun.TaskRunID)
}
