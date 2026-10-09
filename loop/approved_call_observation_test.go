package loop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func restoredStateAfterHolds(t *testing.T, decide func(store *taskstate.TaskRunService, taskRunID string)) agentTaskState {
	t.Helper()
	store := taskstate.NewTaskRunService(taskstate.NewTaskEventService())
	taskRun := store.CreateTaskRun("person-sample", "conversation-sample", "어제 출근을 외부로 바꿔줘")
	decide(store, taskRun.TaskRunID)
	state, errorValue := restoreAgentTaskState(AgentTurnRequest{IsRuntimeRestartResume: true}, TurnOptions{}, taskRun, store.ListTaskEvent(taskRun.TaskRunID))
	if errorValue != nil {
		t.Fatalf("restore: %v", errorValue)
	}
	return state
}

func approvedCallSample() agentcontract.HeldCall {
	return agentcontract.HeldCall{
		ToolName:     "attendance_update",
		ToolInput:    json.RawMessage(`{"location":"external","reason":"현지 출근"}`),
		Confirmation: "어제 출근 기록의 장소를 외부로 바꿀까요?",
	}
}

func TestAResumedRunIsToldTheApprovedCallItHasNotRunYet(t *testing.T) {
	state := restoredStateAfterHolds(t, func(store *taskstate.TaskRunService, taskRunID string) {
		hold := holdrecord.Open(store, taskRunID, approvedCallSample(), nil)
		holdrecord.Decide(store, taskRunID, hold.ID, holdrecord.DecisionApprove, "test")
	})

	content := lastObservationContent(state.Observations)
	if !strings.Contains(content, "attendance_update") || !strings.Contains(content, agentcontract.CanonicalToolInput(approvedCallSample().ToolInput)) {
		t.Fatalf("a resumed run read %q, expected the approved call and its exact input, so a rewritten reason is not taken for a new call the requester must approve again", content)
	}
}

func TestASpentOrRejectedHoldIsNotOfferedAgain(t *testing.T) {
	state := restoredStateAfterHolds(t, func(store *taskstate.TaskRunService, taskRunID string) {
		spent := holdrecord.Open(store, taskRunID, approvedCallSample(), nil)
		holdrecord.Decide(store, taskRunID, spent.ID, holdrecord.DecisionApprove, "test")
		holdrecord.SpendApprovedCall(store, taskRunID, approvedCallSample().ToolName, approvedCallSample().ToolInput)
		rejected := holdrecord.Open(store, taskRunID, approvedCallSample(), nil)
		holdrecord.Decide(store, taskRunID, rejected.ID, holdrecord.DecisionReject, "test")
	})

	for _, observation := range state.Observations {
		if strings.Contains(observation.Output.Content, "has not run yet") {
			t.Fatalf("a run was offered %q for a call that already ran or was refused", observation.Output.Content)
		}
	}
}

func lastObservationContent(observations []turnObservation) string {
	if len(observations) == 0 {
		return ""
	}
	return observations[len(observations)-1].Output.Content
}
