package loop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

// The production provider answers recovery chat completions, so a stand-in that does not would
// let the notice generator give up before it ever reaches the model and hide an unbounded call.
type recoveringLanguageModel struct {
	mutex                sync.Mutex
	recoveryCallCount    int
	recoveryContextError error
	recoveryDeadline     time.Time
	hasRecoveryDeadline  bool
}

func (languageModel *recoveringLanguageModel) GenerateResponse(responseContext context.Context, _ string) (string, error) {
	<-responseContext.Done()
	return "", responseContext.Err()
}

func (languageModel *recoveringLanguageModel) GenerateStructuredResponse(responseContext context.Context, _ model.StructuredResponseRequest) (model.StructuredResponse, error) {
	<-responseContext.Done()
	return model.StructuredResponse{}, responseContext.Err()
}

func (languageModel *recoveringLanguageModel) GenerateChatCompletion(responseContext context.Context, _ model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	<-responseContext.Done()
	return model.ChatCompletionResponse{}, responseContext.Err()
}

func (languageModel *recoveringLanguageModel) GenerateRecoveryChatCompletion(responseContext context.Context, _ model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	languageModel.recordRecoveryCall(responseContext)
	return model.ChatCompletionResponse{
		FinishReason:    "stop",
		SelectedBackend: "remote",
		Message:         model.ChatCompletionMessage{Role: "assistant", Content: "작업을 마치지 못했습니다. 다시 시도해 주세요."},
	}, nil
}

func (languageModel *recoveringLanguageModel) GenerateLocalRecoveryChatCompletion(responseContext context.Context, request model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	return languageModel.GenerateRecoveryChatCompletion(responseContext, request)
}

func (languageModel *recoveringLanguageModel) recordRecoveryCall(responseContext context.Context) {
	languageModel.mutex.Lock()
	defer languageModel.mutex.Unlock()
	languageModel.recoveryCallCount++
	languageModel.recoveryContextError = responseContext.Err()
	languageModel.recoveryDeadline, languageModel.hasRecoveryDeadline = responseContext.Deadline()
}

func TestATurnThatEndsWithNoNoticeLeavesNoRunningTaskRun(t *testing.T) {
	languageModel := &recoveringLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	result, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "무슨 일이 있었는지 알려줘",
		ToolSet:           newTestToolSet(nil),
	})

	if errorValue != nil {
		t.Fatalf("an abandoned turn reports through the result, not an error: %v", errorValue)
	}
	storedTaskRun, isFound := services.taskRunService.FindTaskRun(result.TaskRun.TaskRunID)
	if !isFound || storedTaskRun.Status == agentcontract.TaskStatusRunning {
		t.Fatalf("stored task run = %+v, found = %v, want a status nothing is working on", storedTaskRun, isFound)
	}
	if strings.TrimSpace(result.UserNotice) == "" {
		t.Fatal("the requester has to be told something true about the turn that stopped")
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(storedTaskRun.TaskRunID), agentcontract.TaskEventAgentTurnAbandoned, "") {
		t.Fatal("the ledger has to say why the turn stopped")
	}
	if languageModel.recoveryCallCount == 0 {
		t.Fatal("the notice has to be generated through the model the way production generates it")
	}
}

func TestAnAbandonedTurnGeneratesItsNoticeOnABoundedContext(t *testing.T) {
	languageModel := &recoveringLanguageModel{}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{MaxElapsedSecond: 30})
	runContext, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	if _, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "멈춘 작업을 설명해줘",
		ToolSet:           newTestToolSet(nil),
	}); errorValue != nil {
		t.Fatalf("an abandoned turn reports through the result, not an error: %v", errorValue)
	}

	if languageModel.recoveryContextError != nil {
		t.Fatalf("the notice cannot be generated on the context that just died: %v", languageModel.recoveryContextError)
	}
	if !languageModel.hasRecoveryDeadline {
		t.Fatal("a notice generated on the wedged path with no deadline holds the turn, and the conversation, open forever")
	}
	if remaining := time.Until(languageModel.recoveryDeadline); remaining > maximumElapsedClosingDuration {
		t.Fatalf("the notice outlives the closing ceiling by %s", remaining-maximumElapsedClosingDuration)
	}
}

func TestTheClosingNoticeContextOutlivesTheCallerButNotItsCeiling(t *testing.T) {
	parentContext, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	noticeContext, cancelNotice := closingNoticeContextWithParent(parentContext, AgentTurnRequest{RequesterPersonID: "person-1", ConversationID: "conversation-1"})
	defer cancelNotice()

	if noticeContext.Err() != nil {
		t.Fatalf("the caller's cancellation must not reach the notice: %v", noticeContext.Err())
	}
	deadline, hasDeadline := noticeContext.Deadline()
	if !hasDeadline || time.Until(deadline) > maximumElapsedClosingDuration {
		t.Fatalf("deadline = %v, hasDeadline = %v, want a bound within the closing ceiling", deadline, hasDeadline)
	}
	if model.RequestContextFromContext(noticeContext).RequesterPersonID != "person-1" {
		t.Fatal("the notice call still belongs to the requester who asked")
	}
}

func TestAFinishedReplySurvivesACompletingTransitionThatWillNotStick(t *testing.T) {
	services := newTurnRunnerTestServices(&recoveringLanguageModel{}, TurnOptions{})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "보고서 정리")
	if _, errorValue := services.taskRunService.AdvanceTaskRun(taskRun.TaskRunID, "assistant"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if _, isInterrupted := services.taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, "runtime no longer owns this execution"); !isInterrupted {
		t.Fatal("expected the run to be interruptible without a turn on it")
	}
	reply := "정리한 결과는 이렇습니다."

	result := services.runner.completeTaskRunBestEffort(context.Background(), taskRun.TaskRunID, taskRun.TaskRunID+":turn-001", "finish", AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		ToolSet:           newTestToolSet(nil),
	}, nil, completionGateResult{}, reply)

	if result.TaskRun.Status == agentcontract.TaskStatusCompleted {
		t.Fatal("the completing transition could not have stuck from interrupted")
	}
	if result.TaskRun.Status == "" {
		t.Fatalf("a zero task run is routed as a user notice and then suppressed: %+v", result.TaskRun)
	}
	if result.UserNotice != reply {
		t.Fatalf("user notice = %q, want the reply the agent finished with", result.UserNotice)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), agentcontract.TaskEventAgentCompletionPersistFailed, "") {
		t.Fatal("the ledger has to say the completion did not persist")
	}
}
func TestATurnHoldsItsLeaseFromTheMomentTheRunReadsAsRunning(t *testing.T) {
	services := newTurnRunnerTestServices(&recoveringLanguageModel{}, TurnOptions{MaxElapsedSecond: 30})
	sawLiveTurnWhenRunningBegan := false
	unregisterObserver := services.taskRunService.RegisterTaskRunTransitionObserver(func(taskRun agentcontract.TaskRun) {
		if taskRun.Status != agentcontract.TaskStatusRunning {
			return
		}
		sawLiveTurnWhenRunningBegan = services.taskRunService.IsTaskRunActuallyRunning(taskRun)
	})
	defer unregisterObserver()
	runContext, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	if _, errorValue := services.runner.RunTurn(runContext, AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "시작하자마자 끝난 작업",
		ToolSet:           newTestToolSet(nil),
	}); errorValue != nil {
		t.Fatalf("an abandoned turn reports through the result, not an error: %v", errorValue)
	}

	if !sawLiveTurnWhenRunningBegan {
		t.Fatal("a run that reads as running before its turn takes the lease is interruptible under a live turn")
	}
}
