package loop

import (
	"context"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"strings"
	"testing"
)

func TestAgentTurnRunnerAllowsCorrectedRetryAfterSafeFailure(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"Dana","message":"please take a look"}}`,
		`{"action":"continue","toolName":"message_send","toolInput":{"targetType":"directMessage","personHint":"Dana Lee","message":"please take a look"}}`,
		finishMessageWithEvidence("sent", "obs-003", "message_send", 0),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{RecoveryAttemptLimit: 3})
	toolRegistry := newTestCapabilityToolSet([]string{"message_send"})
	callCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "message_send"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		callCount++
		if callCount == 1 {
			return structuredFailureToolResult("temporary user lookup timeout", "temporary user lookup timeout", "mattermost_unavailable", "mattermost_lookup", true, true), nil
		}
		return testToolSuccess(`{"dispatchID":"post-1"}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID:     "person-1",
		ConversationID:        "conversation-1",
		Prompt:                "send dm",
		ToolSet:               toolRegistry,
		PinnedToolNames:       toolRegistry.ListToolNames(),
		RequiredEvidenceTools: []string{"message_send"},
		OutcomeContract:       OutcomeContract{RequiredEvidenceTools: []string{"message_send"}},
	})
	if errorValue != nil {
		t.Fatalf("expected retry recovery: %v", errorValue)
	}
	if callCount != 2 {
		t.Fatalf("expected corrected retry, got %d calls", callCount)
	}
	if result.FinishMessage != "sent" {
		t.Fatalf("expected final reply after corrected retry, got %q", result.FinishMessage)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_attempt", "corrected_retry") {
		t.Fatal("expected corrected retry event")
	}
}

func TestRecoveryAttemptCountOnlyIncludesSpentInterventions(t *testing.T) {
	failure := newFailureObservation("obs-001", "continue", "message_send", "failed", toolcontract.FailureExternalService, toolcontract.FailureCodes.OperationFailed, "message_send")
	passiveGuidance := recoveryGuidanceObservation(nil, 2, failure, "")
	spentGuidance := recoveryGuidanceObservation(nil, 3, failure, "")
	spentGuidance.RecoveryAttemptSpent = true
	retryObservation := failure
	retryObservation.ObservationID = "obs-004"
	retryObservation.RecoveryAttemptKey = "message_send\x00{}"
	retryObservation.RecoveryAttemptSpent = true

	if count := recoveryAttemptCount([]turnObservation{failure, passiveGuidance}); count != 0 {
		t.Fatalf("expected passive guidance not to spend recovery budget, got %d", count)
	}
	if count := recoveryAttemptCount([]turnObservation{failure, passiveGuidance, spentGuidance, retryObservation}); count != 2 {
		t.Fatalf("expected spent guidance and retry to consume budget, got %d", count)
	}
}

func TestAgentTurnRunnerAllowsInspectionAfterAdjacentRecoveryBudgetExhausted(t *testing.T) {
	languageModel := &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"site.build","toolInput":{"siteID":"site-1"}}`,
		`{"action":"continue","toolName":"file_read","toolInput":{"path":"home/sites/site-1/draft/app/src/App.tsx"}}`,
		finishMessageDocument("Checked."),
	}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{
		MaxIterationCount: 6,
		MaxToolCallCount:  4,
		RecoveryBudget: RecoveryBudget{
			CorrectedRetry: 0,
			AlternateRoute: 0,
			AdjacentTool:   -1,
			NoToolFallback: 0,
		},
	})
	toolRegistry := newHybridKernelCapabilityToolSet([]string{"file_read", "file_edit"}, []string{"site.build"})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "site.build"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return toolcontract.ToolResult{
			Output: toolcontract.ToolOutput{Content: "source failed"},
			Failure: &toolcontract.ToolFailure{
				Kind:            toolcontract.FailureInvalidInput,
				Code:            toolcontract.FailureCodes.InvalidInput.String(),
				Stage:           "site_build_source",
				UserSafeSummary: "site source failed",
				Retryable:       true,
				FailureClass:    failureClassQuality,
				RetryPolicy:     retryPolicyAfterPrecondition,
				RecoveryHints:   []toolcontract.RecoveryHint{{Action: "edit_resource", ToolNames: []string{"file_read", "file_edit"}}},
			},
		}, nil
	})
	fileReadCount := 0
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_read", SideEffectClass: toolcontract.ToolSideEffectRead}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		fileReadCount++
		return testToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","content":"broken"}`), nil
	})
	registerTestTool(toolRegistry, toolcontract.ToolDefinition{Name: "file_edit"}, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		return testToolSuccess(`{"path":"home/sites/site-1/draft/app/src/App.tsx","matchCount":1}`), nil
	})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "look into the site build problem",
		ToolSet:           toolRegistry,
		PinnedToolNames:   []string{"site.build", "file_read", "file_edit"},
	})
	if errorValue != nil {
		t.Fatalf("expected inspection recovery to continue: %v", errorValue)
	}
	if fileReadCount != 1 {
		t.Fatalf("expected file_read to run despite exhausted adjacent budget, got %d", fileReadCount)
	}
	events := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(events, "agent.recovery_budget_exhausted", "file_read") {
		t.Fatal("did not expect inspection tool to be blocked by adjacent recovery budget")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "inspection") {
		t.Fatal("expected inspection recovery event")
	}
	if !taskEventsContain(events, "agent.recovery_attempt", "precondition") {
		t.Fatal("expected precondition recovery event")
	}
}

type calendarCallRecording struct {
	deleteCallCount int
	updateCallCount int
}

func newCalendarRecoveryToolRegistry(recording *calendarCallRecording) *toolcontract.ToolSet {
	deleteDefinition := namespacedToolDefinition("calendar_delete", "calendar", toolcontract.ToolSideEffectStateChange)
	updateDefinition := namespacedToolDefinition("calendar_update", "calendar", toolcontract.ToolSideEffectStateChange)
	listDefinition := namespacedToolDefinition("calendar_list", "calendar", toolcontract.ToolSideEffectRead)
	toolRegistry := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{deleteDefinition, updateDefinition, listDefinition})
	registerTestTool(toolRegistry, deleteDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		recording.deleteCallCount++
		return structuredFailureToolResult("일정을 찾지 못했습니다", "일정을 찾지 못했습니다", toolcontract.FailureCodes.NotFound.String(), "calendar_lookup", false, false), nil
	})
	registerTestTool(toolRegistry, updateDefinition, func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
		recording.updateCallCount++
		return testToolSuccess(`{"eventID":"event-2"}`), nil
	})
	return toolRegistry
}

func calendarDeleteFailureReportDocument() string {
	return failureReportDocument(
		"지난 워크숍 회고 일정을 찾지 못해 삭제하지 못했습니다.",
		"calendar_delete",
		"지난 워크숍 회고",
		toolcontract.FailureCodes.NotFound.String(),
		"calendar_lookup",
		"일정을 찾지 못했습니다",
	)
}

func twoRequestCalendarLanguageModel(secondRequestEventHint string) *sequenceLanguageModel {
	return &sequenceLanguageModel{contents: []string{
		`{"action":"continue","toolName":"calendar_delete","toolInput":{"eventHint":"지난 워크숍 회고"}}`,
		`{"action":"continue","toolName":"calendar_update","toolInput":{"eventHint":"` + secondRequestEventHint + `","people":["이샘플","박예시"]}}`,
		calendarDeleteFailureReportDocument(),
	}}
}

func runTwoRequestCalendarTurn(t *testing.T, languageModel *sequenceLanguageModel, options TurnOptions) (turnRunnerTestServices, AgentTurnResult, *calendarCallRecording) {
	t.Helper()
	recording := &calendarCallRecording{}
	services := newTurnRunnerTestServices(languageModel, options)
	toolRegistry := newCalendarRecoveryToolRegistry(recording)
	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "지난 워크숍 회고 일정은 지우고, 상하이 생산 미팅에는 이샘플 님을 넣어줘",
		ToolSet:           toolRegistry,
		PinnedToolNames:   toolRegistry.ListToolNames(),
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to finish: %v", errorValue)
	}
	return services, result, recording
}

func TestWorkOnADifferentEventRunsEvenWhenTheRecoveryBudgetIsGone(t *testing.T) {
	services, result, recording := runTwoRequestCalendarTurn(t, twoRequestCalendarLanguageModel("상하이 생산 미팅"), TurnOptions{
		MaxIterationCount: 8,
		MaxToolCallCount:  6,
		RecoveryBudget:    terminalNoToolRecoveryBudgetForTest(),
	})

	if recording.updateCallCount != 1 {
		t.Fatalf("expected the second request to run once despite the failed delete's exhausted budget, got %d calls", recording.updateCallCount)
	}
	if countTaskEvents(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_budget_exhausted") != 0 {
		t.Fatal("work on another event must never be refused by the failed call's recovery budget")
	}
}

func TestTheSecondRequestInOneMessageRunsAfterTheFirstOneFails(t *testing.T) {
	services, result, recording := runTwoRequestCalendarTurn(t, twoRequestCalendarLanguageModel("상하이 생산 미팅"), TurnOptions{
		MaxIterationCount: 8,
		MaxToolCallCount:  6,
		RecoveryBudget:    defaultRecoveryBudget(),
	})

	if recording.updateCallCount != 1 {
		t.Fatalf("expected the second request to run exactly once, got %d calls", recording.updateCallCount)
	}
	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if countTaskEvents(taskEvents, "agent.recovery_budget_exhausted") != 0 {
		t.Fatal("the second request must not be refused")
	}
	if taskEventsContain(taskEvents, "task.paused", "max_iterations") {
		t.Fatal("a two-item request must not thrash until the iteration ceiling")
	}
}

func TestTheAgentStillReportsTheDeleteItCouldNotDo(t *testing.T) {
	services, result, _ := runTwoRequestCalendarTurn(t, twoRequestCalendarLanguageModel("상하이 생산 미팅"), TurnOptions{
		MaxIterationCount: 8,
		MaxToolCallCount:  6,
		RecoveryBudget:    defaultRecoveryBudget(),
	})

	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.failure_report_facts_used", "calendar_delete") {
		t.Fatal("finishing the second request must not settle the first request's failure in silence")
	}
}

func runRefusedSameEventCalendarTurn(t *testing.T) (turnRunnerTestServices, AgentTurnResult, *calendarCallRecording) {
	t.Helper()
	return runTwoRequestCalendarTurn(t, twoRequestCalendarLanguageModel("지난 워크숍 회고"), TurnOptions{
		MaxIterationCount: 8,
		MaxToolCallCount:  6,
		RecoveryBudget:    terminalNoToolRecoveryBudgetForTest(),
	})
}

func TestRepeatedAttemptsAtTheSameFailedTargetStillRunOutOfBudget(t *testing.T) {
	services, result, recording := runRefusedSameEventCalendarTurn(t)

	if recording.updateCallCount != 0 {
		t.Fatalf("another route to the event that just failed stays rationed, got %d calls", recording.updateCallCount)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_budget_exhausted", recoveryStepAlternateRoute) {
		t.Fatal("expected the alternate route to the same event to be refused")
	}
}

func TestTheRefusalNamesTheToolItRefused(t *testing.T) {
	services, result, _ := runRefusedSameEventCalendarTurn(t)

	if !taskEventsContain(services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID), "agent.recovery_budget_exhausted", `"tool":"calendar_update"`) {
		t.Fatal("the refusal must name the call it refused, not the call that failed")
	}
}

func TestRecoveryGuidanceDoesNotCopyTheRequestBackIn(t *testing.T) {
	instruction := "How many likes did all Venmo transactions, I sent this month, have in total?"
	observation := turnObservation{
		ObservationID: "obs-004",
		Action:        "continue",
		Tool:          "terminal_run",
		Failure: &toolcontract.ToolFailure{
			Kind:            toolcontract.FailureUnknown,
			Code:            toolcontract.FailureCodes.OperationFailed.String(),
			Stage:           "terminal_run",
			UserSafeSummary: "the command exited 1",
		},
	}

	guidance := recoveryGuidanceObservation(nil, 5, observation, instruction)

	if strings.Contains(guidance.ContentText(), instruction) {
		t.Fatal("the request is already this turn's user message, and every recovery keeping its own copy rides in every prompt after it")
	}
	if !strings.Contains(guidance.ContentText(), "do not drift") {
		t.Fatalf("the reminder not to drift is what the copy was carrying: %q", guidance.ContentText())
	}
}
