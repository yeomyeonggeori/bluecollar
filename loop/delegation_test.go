package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func delegateActionDocument(instruction string, expectedResult string) string {
	return `{"action":"delegate","instruction":"` + instruction + `","expectedResult":"` + expectedResult + `"}`
}

func TestDelegationCostsNothingUntilAHostAsksForIt(t *testing.T) {
	request := AgentTurnRequest{ToolSet: newTestToolSet([]string{toolcontract.ShellToolName})}

	withoutDelegation := buildAgentSystemInstruction(request, TurnOptions{}).Text()
	if strings.Contains(withoutDelegation, "Delegation:") {
		t.Fatal("a host that never delegates pays for the paragraph on every step of every task forever")
	}
	schemaWithoutDelegation := buildActionSchemaFromToolDefinitions(nil, nil, false, nil, false, true, true, false)
	if strings.Contains(schemaWithoutDelegation, "delegate") {
		t.Fatalf("and pays for the schema variant too: %s", schemaWithoutDelegation)
	}

	withDelegation := buildAgentSystemInstruction(request, TurnOptions{DelegationLimit: 2}).Text()
	if !strings.Contains(withDelegation, "may delegate 2 times in total") {
		t.Fatalf("a limit the model is not told about is a limit it discovers by hitting it: %s", withDelegation)
	}
	if !strings.Contains(buildActionSchemaFromToolDefinitions(nil, nil, false, nil, false, true, true, true), `"delegate"`) {
		t.Fatal("the action has to be in the schema the model answers with")
	}
}

func TestADelegatedTurnReportsBackThroughTheParentsLedger(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		modelTier: "xlow",
		contents: []string{
			delegateActionDocument("read the release notes and say what changed", "one sentence"),
			finishMessageDocument("the release renamed two flags"),
			finishMessageDocument("두 개의 플래그 이름이 바뀌었습니다"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 4, MaxToolCallCount: 5, DelegationLimit: 2})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "무엇이 바뀌었는지 알려줘",
		ToolSet:           newTestToolSet([]string{toolcontract.ShellToolName}),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, "delegate.launched", "read the release notes") {
		t.Fatalf("a child the parent cannot account for is work nobody can audit: %d events", len(taskEvents))
	}
	if !taskEventsContain(taskEvents, "delegate.finished", "childTaskRunID") {
		t.Fatal("and the parent's ledger names the run that did it")
	}
	if !strings.Contains(strings.Join(promptsOf(languageModel), "\n"), "the release renamed two flags") {
		t.Fatal("the parent has to see what the child reported, or delegating it lost the work")
	}
}

func TestADelegatedChildFromAResumedParentGetsItsOwnTaskRun(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		modelTier: "xlow",
		contents: []string{
			delegateActionDocument("read the release notes and say what changed", "one sentence"),
			finishMessageDocument("the release renamed two flags"),
			finishMessageDocument("두 개의 플래그 이름이 바뀌었습니다"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 4, MaxToolCallCount: 5, DelegationLimit: 2})
	parentTaskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "무엇이 바뀌었는지 알려줘")

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:      "person-1",
		ExistingTaskRunID:      parentTaskRun.TaskRunID,
		IsApprovalContinuation: true,
		ConversationID:         "conversation-1",
		Prompt:                 "무엇이 바뀌었는지 알려줘",
		ToolSet:                newTestToolSet([]string{toolcontract.ShellToolName}),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	if result.TaskRun.TaskRunID != parentTaskRun.TaskRunID {
		t.Fatalf("the resumed parent turn must keep its own run, got %s want %s", result.TaskRun.TaskRunID, parentTaskRun.TaskRunID)
	}

	parentTaskEvents := services.taskEventService.ListTaskEvent(parentTaskRun.TaskRunID)
	childTaskRunID := ""
	for _, taskEvent := range parentTaskEvents {
		if taskEvent.Name != "delegate.finished" {
			continue
		}
		var document struct {
			ChildTaskRunID string `json:"childTaskRunID"`
		}
		if json.Unmarshal([]byte(taskEvent.Body), &document) == nil {
			childTaskRunID = document.ChildTaskRunID
		}
	}
	if childTaskRunID == "" {
		t.Fatal("delegate.finished has to name the run that actually did the work")
	}
	if childTaskRunID == parentTaskRun.TaskRunID {
		t.Fatal("a child spawned from a resumed parent turn must not execute on the parent's own run")
	}
	if countTaskEvents(parentTaskEvents, agentcontract.TaskEventTaskCompleted) != 1 {
		t.Fatalf("the parent's run must be completed once, by the parent, not once more by the child: %d", countTaskEvents(parentTaskEvents, agentcontract.TaskEventTaskCompleted))
	}

	childTaskRun, isFound := services.taskRunService.FindTaskRun(childTaskRunID)
	if !isFound {
		t.Fatal("the child's own run has to be findable on its own ID")
	}
	if childTaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("expected the child's own run to complete, got %s", childTaskRun.Status)
	}
}

func TestAChildDoesNotDelegateAgainAndSpendsTheParentsBudget(t *testing.T) {
	parentOptions := TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 10, MaxToolCallCount: 8, MaxElapsedSecond: 600, DelegationLimit: 2}
	runner := NewAgentTurnRunner(nil, nil, nil, nil, parentOptions)
	state := &agentTaskState{IterationCount: 4, ToolCallCount: 3}

	childOptions := runner.childRunner(state).options

	if childOptions.DelegationLimit != 0 {
		t.Fatal("one request must not become a tree nobody sized")
	}
	if childOptions.MaxIterationCount != 6 || childOptions.MaxToolCallCount != 5 {
		t.Fatalf("a child that gets its own full ceiling doubles what the task was allowed to spend: %+v", childOptions)
	}
}

func TestDelegationStopsAtTheLimitAndSaysWhy(t *testing.T) {
	runner := NewAgentTurnRunner(nil, nil, nil, nil, TurnOptions{DelegationLimit: 1})
	state := &agentTaskState{Observations: []turnObservation{{ObservationID: "obs-1", Action: "delegate"}}}

	observation := runner.runDelegatedTurn(context.Background(), "task-1", state, turnActionDocument{Action: "delegate", Instruction: "do the other half"})

	if !observation.Failed() || !strings.Contains(observation.ContentText(), "all 1 of the delegations") {
		t.Fatalf("a refusal the model cannot read is a refusal it will make again next step: %+v", observation)
	}
}

func TestADelegatedChildIsDeniedTheApprovalItCannotAskFor(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		modelTier: "xlow",
		contents: []string{
			delegateActionDocument("delete the duplicate event", "one sentence"),
			directToolAction("continue", "", "calendar_delete", `{"eventHint":"event-1"}`),
			finishMessageDocument("중복 일정은 승인이 필요해 지우지 못했습니다"),
			finishMessageDocument("중복 일정은 승인이 필요해 그대로 두었습니다"),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 6, MaxToolCallCount: 5, DelegationLimit: 2})
	toolRegistry := newTestCapabilityToolSet([]string{"calendar_delete"})
	invokedInputs := []string{}
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "calendar_delete", RequiresApproval: true}, func(_ context.Context, invocation toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		invokedInputs = append(invokedInputs, string(invocation.Input))
		return testToolSuccess(`{"eventID":"event-1","status":"deleted"}`), nil
	})
	toolRegistry.UseToolCallGate(holdingToolCallGate{
		taskRunService: services.taskRunService,
		confirmation:   "이 일정을 삭제할까요?",
		denialNotice:   "이 호출은 요청자의 승인이 필요합니다",
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "중복 일정 정리해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"calendar_delete"},
		WorkspaceRootPath: t.TempDir(),
	})

	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	if len(invokedInputs) != 0 {
		t.Fatalf("a denied call that still reaches its handler has already had the effect, got %+v", invokedInputs)
	}
	for _, taskRun := range services.taskRunService.ListTaskRun() {
		if taskRun.Status == agentcontract.TaskStatusWaitingApproval {
			t.Fatalf("a child left waiting for an approval nobody was asked for is a run a later approve can hijack: %s", taskRun.TaskRunID)
		}
	}
	if result.TaskRun.Status != agentcontract.TaskStatusCompleted {
		t.Fatalf("a child that was told no is not a parent that failed, got %s", result.TaskRun.Status)
	}
	if !strings.Contains(strings.Join(promptsOf(languageModel), "\n"), "이 호출은 요청자의 승인이 필요합니다") {
		t.Fatal("the child's model has to read the denial, or it waits for an approval that never comes")
	}
}

func TestAStoppedChildsArtifactsSurviveIntoTheParent(t *testing.T) {
	languageModel := &sequenceLanguageModel{
		modelTier: "xlow",
		contents: []string{
			delegateActionDocument("write the release notes", "the file path"),
			directToolAction("continue", "", "notes_write", `{"path":"release-notes.md"}`),
			directToolAction("continue", "", "notes_write", `{"path":"release-notes.md"}`),
			directToolAction("continue", "", "notes_write", `{"path":"release-notes.md"}`),
		},
	}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 6, MaxToolCallCount: 4, DelegationLimit: 1})
	toolRegistry := newTestCapabilityToolSet([]string{"notes_write"})
	writeCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "notes_write"}, func(toolContext context.Context, _ toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		writeCount++
		if writeCount > 1 {
			services.taskRunService.CancelTaskRun(toolcontract.TaskRunIDFromContext(toolContext), "person-1")
			return testToolSuccess(`{"status":"cancelled"}`), nil
		}
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: `{"devicePath":"/tmp/internkim-companion-files/release-notes.md"}`},
			Attachments: []toolcontract.FileAttachment{{
				DevicePath:  "/tmp/internkim-companion-files/release-notes.md",
				Filename:    "release-notes.md",
				ContentType: "text/markdown",
				SizeBytes:   12,
			}},
		}, nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "릴리스 노트 정리해줘",
		ResponseLanguage:  ResponseLanguageKorean,
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"notes_write"},
		WorkspaceRootPath: t.TempDir(),
	})

	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}
	if !hasAttachmentDevicePath(result.Attachments, "/tmp/internkim-companion-files/release-notes.md") {
		t.Fatalf("work a stopped child finished is work the requester paid for, and the parent threw it away: %+v", result.Attachments)
	}
}
