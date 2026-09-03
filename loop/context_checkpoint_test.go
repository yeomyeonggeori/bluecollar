package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func toolResultTaskEvent(taskEventID string, observationID string) agentcontract.TaskEvent {
	return agentcontract.TaskEvent{
		TaskEventID: taskEventID,
		Name:        "tool.note_write.result",
		Body: marshalEventBody(turnObservation{
			ObservationID: observationID,
			Action:        "continue",
			Tool:          "note_write",
			Output:        toolcontract.ToolOutput{Content: "wrote " + observationID},
		}),
	}
}

func unreadableTaskEvent(taskEventID string) agentcontract.TaskEvent {
	return agentcontract.TaskEvent{TaskEventID: taskEventID, Name: "tool.note_write.result", Body: "{this is not decodable"}
}

func checkpointTaskEvent(summary TaskContextSummary) agentcontract.TaskEvent {
	return agentcontract.TaskEvent{
		TaskEventID: "event-checkpoint",
		Name:        agentcontract.TaskEventAgentContextSummary,
		Body:        marshalEventBody(summary),
	}
}

func retainedContextCheckpoint() TaskContextSummary {
	return TaskContextSummary{
		ObservationID:                 "context-summary-observation-3",
		CompactedThroughObservationID: "observation-3",
		CompactedObservationIDs:       []string{"observation-1", "observation-2", "observation-3"},
		AccountedTaskEventIDs:         []string{"event-1", "event-2", "event-3", "event-4"},
		RetainedObservations:          []turnObservation{{ObservationID: "observation-4", Action: "continue", Tool: "note_write"}},
		CompactedObservationCount:     3,
		CompactedToolCallCount:        3,
		Goal:                          "ship",
	}
}

func TestAResumeRebuildsContextFromTheCheckpointAloneWithoutTheEventsItAccountsFor(t *testing.T) {
	events := []agentcontract.TaskEvent{
		unreadableTaskEvent("event-1"),
		unreadableTaskEvent("event-2"),
		unreadableTaskEvent("event-3"),
		unreadableTaskEvent("event-4"),
		checkpointTaskEvent(retainedContextCheckpoint()),
		toolResultTaskEvent("event-5", "observation-5"),
	}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "ship"}, TurnOptions{}, agentcontract.TaskRun{TaskRunID: "task-1", Status: agentcontract.TaskStatusRunning}, events)

	if errorValue != nil {
		t.Fatalf("expected the resume to rebuild state: %v", errorValue)
	}
	if len(state.Observations) != 2 {
		t.Fatalf("expected the retained observation and the one after the checkpoint, got %d", len(state.Observations))
	}
	if state.Observations[0].ObservationID != "observation-4" || state.Observations[1].ObservationID != "observation-5" {
		t.Fatalf("expected observation-4 then observation-5, got %s and %s", state.Observations[0].ObservationID, state.Observations[1].ObservationID)
	}
}

func TestACheckpointDoesNotLoseTheWorkItAbsorbed(t *testing.T) {
	events := []agentcontract.TaskEvent{
		toolResultTaskEvent("event-1", "observation-1"),
		toolResultTaskEvent("event-2", "observation-2"),
		toolResultTaskEvent("event-3", "observation-3"),
		toolResultTaskEvent("event-4", "observation-4"),
		checkpointTaskEvent(retainedContextCheckpoint()),
		toolResultTaskEvent("event-5", "observation-5"),
	}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "ship"}, TurnOptions{}, agentcontract.TaskRun{TaskRunID: "task-1", Status: agentcontract.TaskStatusRunning}, events)

	if errorValue != nil {
		t.Fatalf("expected the resume to rebuild state: %v", errorValue)
	}
	if state.IterationCount != 5 {
		t.Fatalf("compaction must not give the task iterations back; expected 5, got %d", state.IterationCount)
	}
	if state.ToolCallCount != 5 {
		t.Fatalf("compaction must not hide completed tool calls; expected 5, got %d", state.ToolCallCount)
	}
}

func TestATaskWithNoCheckpointStillResumesFromItsWholeHistory(t *testing.T) {
	events := []agentcontract.TaskEvent{
		toolResultTaskEvent("event-1", "observation-1"),
		toolResultTaskEvent("event-2", "observation-2"),
	}

	state, errorValue := restoreAgentTaskState(AgentTurnRequest{Prompt: "ship"}, TurnOptions{}, agentcontract.TaskRun{TaskRunID: "task-1", Status: agentcontract.TaskStatusRunning}, events)

	if errorValue != nil {
		t.Fatalf("expected the resume to rebuild state: %v", errorValue)
	}
	if len(state.Observations) != 2 || state.IterationCount != 2 {
		t.Fatalf("expected the full history, got %d observations and iteration count %d", len(state.Observations), state.IterationCount)
	}
}

func TestTheCheckpointBookkeepingNeverReachesTheModel(t *testing.T) {
	renderedSummary := summaryObservation(retainedContextCheckpoint()).ContentText()

	if strings.Contains(renderedSummary, "accountedTaskEventIDs") || strings.Contains(renderedSummary, "retainedObservations") {
		t.Fatalf("the checkpoint's restore bookkeeping is not context for the model, got %s", renderedSummary)
	}
	if !strings.Contains(renderedSummary, "ship") {
		t.Fatalf("expected the summary the model reads to survive, got %s", renderedSummary)
	}
}

func TestASecondCheckpointInheritsWhatTheFirstAlreadyAccountedFor(t *testing.T) {
	events := []agentcontract.TaskEvent{
		toolResultTaskEvent("event-1", "observation-1"),
		toolResultTaskEvent("event-2", "observation-2"),
		toolResultTaskEvent("event-3", "observation-3"),
		toolResultTaskEvent("event-4", "observation-4"),
		toolResultTaskEvent("event-5", "observation-5"),
	}
	secondPlan := taskContextCompactionPlan{
		CompactableObservations:       []turnObservation{{ObservationID: "observation-4", Action: "continue", Tool: "note_write"}},
		CompactedObservationIDs:       []string{"observation-4"},
		CompactedThroughObservationID: "observation-4",
	}
	retainedByTheSecondCheckpoint := []turnObservation{
		{ObservationID: "context-summary-observation-4", Action: "context_summary"},
		{ObservationID: "observation-5", Action: "continue", Tool: "note_write"},
	}

	secondCheckpoint := summaryAccountingForCompactedObservations(
		TaskContextSummary{ObservationID: "context-summary-observation-4"},
		retainedContextCheckpoint(),
		retainedByTheSecondCheckpoint,
		secondPlan,
		events,
	)

	for _, inheritedTaskEventID := range []string{"event-1", "event-2", "event-3"} {
		if !stringSet(secondCheckpoint.AccountedTaskEventIDs)[inheritedTaskEventID] {
			t.Fatalf("history the first checkpoint absorbed must not come back; %s is unaccounted in %v", inheritedTaskEventID, secondCheckpoint.AccountedTaskEventIDs)
		}
	}
	if secondCheckpoint.CompactedObservationCount != 4 {
		t.Fatalf("expected three inherited plus one newly compacted, got %d", secondCheckpoint.CompactedObservationCount)
	}
	if secondCheckpoint.CompactedToolCallCount != 4 {
		t.Fatalf("expected three inherited plus one newly compacted tool call, got %d", secondCheckpoint.CompactedToolCallCount)
	}
}

func TestRecompactingTheSameObservationDoesNotCountItTwice(t *testing.T) {
	events := []agentcontract.TaskEvent{
		toolResultTaskEvent("event-1", "observation-1"),
		toolResultTaskEvent("event-2", "observation-2"),
		toolResultTaskEvent("event-3", "observation-3"),
		toolResultTaskEvent("event-4", "observation-4"),
	}
	planRepeatingTheWholeHistory := taskContextCompactionPlan{
		CompactableObservations: []turnObservation{
			{ObservationID: "observation-1", Action: "continue", Tool: "note_write"},
			{ObservationID: "observation-2", Action: "continue", Tool: "note_write"},
			{ObservationID: "observation-3", Action: "continue", Tool: "note_write"},
			{ObservationID: "observation-4", Action: "continue", Tool: "note_write"},
		},
		CompactedObservationIDs:       []string{"observation-1", "observation-2", "observation-3", "observation-4"},
		CompactedThroughObservationID: "observation-4",
	}

	secondCheckpoint := summaryAccountingForCompactedObservations(
		TaskContextSummary{ObservationID: "context-summary-observation-4"},
		retainedContextCheckpoint(),
		nil,
		planRepeatingTheWholeHistory,
		events,
	)

	if secondCheckpoint.CompactedObservationCount != 4 {
		t.Fatalf("the warm path recompacts the whole history every time; expected 4, got %d", secondCheckpoint.CompactedObservationCount)
	}
}

func TestACompactedRunResumesToExactlyWhatTheModelWasLastShown(t *testing.T) {
	observations := numberedContextSummaryObservations(14, 2000, "history")
	summaryResponse := `{"goal":"ship","completedSteps":["rolled summary"],"artifacts":[],"keyDecisions":[],"exhaustedRecoveryRoutes":[],"activeFailureDebt":[],"nextPlan":["finish"]}`
	languageModel := &sequenceLanguageModel{contents: []string{summaryResponse, finishMessageDocument("done")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{ContextWindowTokens: 1000})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "ship")
	for _, observation := range observations {
		services.taskEventService.AppendTaskEvent(taskRun.TaskRunID, "tool.note_write.result", marshalEventBody(observation))
	}

	state := agentTaskState{
		Request:      AgentTurnRequest{Prompt: "ship"},
		Options:      services.runner.options,
		Observations: observations,
	}
	promptObservations := services.runner.promptVisibleObservationsForAction(context.Background(), taskRun.TaskRunID, state)

	events := services.taskRunService.ListTaskEvent(taskRun.TaskRunID)
	checkpoint := taskContextSummaryFromTaskEvents(events)
	if !checkpoint.accountsForTaskEvents() {
		t.Fatalf("expected the compaction to write a checkpoint, got %+v", checkpoint)
	}

	resumedState, errorValue := restoreAgentTaskState(
		AgentTurnRequest{Prompt: "ship", IsRuntimeRestartResume: true},
		services.runner.options,
		agentcontract.TaskRun{TaskRunID: taskRun.TaskRunID, Status: agentcontract.TaskStatusRunning},
		events,
	)
	if errorValue != nil {
		t.Fatalf("expected the resume to rebuild state: %v", errorValue)
	}

	shownObservationIDs := observationIDsOf(observationsExcept(promptObservations, checkpoint.ObservationID))
	resumedObservationIDs := observationIDsOf(resumedState.Observations)
	if strings.Join(shownObservationIDs, ",") != strings.Join(resumedObservationIDs, ",") {
		t.Fatalf("a resume must show the model what it was last shown; last shown %v, resumed %v", shownObservationIDs, resumedObservationIDs)
	}
	if resumedState.IterationCount != len(observations) {
		t.Fatalf("expected the resumed run to still own its %d iterations, got %d", len(observations), resumedState.IterationCount)
	}
}

func requestedToolTaskEvent(taskEventID string, observationID string, toolName string) agentcontract.TaskEvent {
	return agentcontract.TaskEvent{
		TaskEventID: taskEventID,
		Name:        agentcontract.ToolTaskEventName(toolName, agentcontract.ToolTaskEventRequestedSuffix),
		Body:        marshalEventBody(map[string]any{"observationID": observationID, "toolName": toolName, "input": map[string]string{"to": "이샘플"}}),
	}
}

func TestARestartSeesACallThatWasStartedAndNeverAnswered(t *testing.T) {
	events := []agentcontract.TaskEvent{
		requestedToolTaskEvent("event-1", "observation-1", "note_write"),
		toolResultTaskEvent("event-2", "observation-1"),
		requestedToolTaskEvent("event-3", "observation-2", "message_send"),
	}

	observations := observationsFromTaskEvents(events)

	if len(observations) != 2 {
		t.Fatalf("the answered call and the interrupted one are both facts about this task: %+v", observations)
	}
	interrupted := observations[1]
	if interrupted.ObservationID != "observation-2" || interrupted.Tool != "message_send" {
		t.Fatalf("expected the unanswered message_send to come back: %+v", interrupted)
	}
	if !interrupted.Failed() {
		t.Fatal("a call whose effect is unknown is unresolved work, and failure debt is how this loop refuses to finish on unresolved work")
	}
	if !strings.Contains(interrupted.ContentText(), "whether it took effect is unknown") {
		t.Fatalf("the model has to be told what it does not know, not just that something failed: %q", interrupted.ContentText())
	}
	if !strings.Contains(string(interrupted.ToolInput), "이샘플") {
		t.Fatalf("the input was recorded before the call and is what says which message may already be sent: %q", interrupted.ToolInput)
	}
}

func TestAnAnsweredCallLeavesNothingBehind(t *testing.T) {
	events := []agentcontract.TaskEvent{
		requestedToolTaskEvent("event-1", "observation-1", "note_write"),
		toolResultTaskEvent("event-2", "observation-1"),
	}

	observations := observationsFromTaskEvents(events)

	if len(observations) != 1 || observations[0].Failed() {
		t.Fatalf("a call that finished is not interrupted, and inventing debt for it would stop every clean restart: %+v", observations)
	}
}
