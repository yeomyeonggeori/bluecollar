package acpagent

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

func TestAHostCancelLeavesAParkedRunParkedAndCancelsARunningOne(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		park           func(openSession *session, taskRunID string)
		expectedStatus agentcontract.TaskStatus
	}{
		{"waiting for an answer", func(openSession *session, taskRunID string) {
			openSession.taskRuns.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingUserInput, "which room?")
		}, agentcontract.TaskStatusWaitingUserInput},
		{"waiting for an approval", func(openSession *session, taskRunID string) {
			openSession.taskRuns.PauseTaskRun(taskRunID, agentcontract.TaskStatusWaitingApproval, "send it?")
		}, agentcontract.TaskStatusWaitingApproval},
		{"running", func(*session, string) {}, agentcontract.TaskStatusCancelled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runningAgent := newTestAgent(t, &scriptedLanguageModel{})
			newSession, errorValue := runningAgent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: t.TempDir()})
			if errorValue != nil {
				t.Fatalf("session/new: %v", errorValue)
			}
			openSession, _ := runningAgent.session(newSession.SessionId)
			taskRun := openSession.taskRuns.CreateTaskRunWithOrigin(requesterPersonID, taskstate.TaskRunOrigin{}, "book a room")
			openSession.taskRuns.AdvanceTaskRun(taskRun.TaskRunID, "default")
			openSession.rememberTaskRun(taskRun.TaskRunID)
			testCase.park(openSession, taskRun.TaskRunID)

			runningAgent.Cancel(context.Background(), acp.CancelNotification{SessionId: newSession.SessionId})

			after, _ := openSession.taskRuns.FindTaskRun(taskRun.TaskRunID)
			if after.Status != testCase.expectedStatus {
				t.Fatalf("a host cancel ends the turn, and the run is %q afterwards where %q was expected", after.Status, testCase.expectedStatus)
			}
		})
	}
}
