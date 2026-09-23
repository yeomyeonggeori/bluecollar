package taskstate

import (
	"errors"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
)

type recordingLLMCallRepository struct {
	events      []agentcontract.TaskEvent
	records     []agentcontract.LLMCallRecord
	insertError error
}

func (repository *recordingLLMCallRepository) InsertLLMCall(taskEvent agentcontract.TaskEvent, record agentcontract.LLMCallRecord) error {
	repository.events = append(repository.events, taskEvent)
	repository.records = append(repository.records, record)
	return repository.insertError
}

type recordingTaskEventRepository struct {
	events []agentcontract.TaskEvent
}

func (repository *recordingTaskEventRepository) InsertTaskEvent(taskEvent agentcontract.TaskEvent) error {
	repository.events = append(repository.events, taskEvent)
	return nil
}

func (repository *recordingTaskEventRepository) ListTaskEvent(string) ([]agentcontract.TaskEvent, error) {
	return repository.events, nil
}

func (repository *recordingTaskEventRepository) ListTaskEventByNameForTaskRuns([]string, string) ([]agentcontract.TaskEvent, error) {
	return repository.events, nil
}

func TestACallWhoseExchangeCannotBeStoredStillKeepsItsRecord(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskEventRepository := &recordingTaskEventRepository{}
	taskEventService.UseRepository(taskEventRepository)
	taskEventService.UseLLMCallRepository(&recordingLLMCallRepository{insertError: errors.New("disk full")})

	taskEventService.AppendLLMCall("run-1", agentcontract.LLMCallRecord{
		Kind:     "text",
		CostUSD:  0.01,
		Exchange: &model.WireExchange{Endpoint: "https://provider.example.test/v1/chat/completions", Request: `{"prompt":"hello"}`},
	})

	if len(taskEventRepository.events) != 1 || taskEventRepository.events[0].Name != agentcontract.TaskEventLLMCall {
		t.Fatalf("expected the call's record kept in the event ledger, got %+v", taskEventRepository.events)
	}
}

func TestAppendLLMCallHandsTheExchangeToItsRepository(t *testing.T) {
	taskEventService := NewTaskEventService()
	llmCallRepository := &recordingLLMCallRepository{}
	taskEventService.UseLLMCallRepository(llmCallRepository)
	observed := []string{}
	taskEventService.RegisterTaskRunObserver("run-1", func(rawTurnEvent RawTurnEvent) {
		observed = append(observed, rawTurnEvent.Name)
	})

	taskEventService.AppendLLMCall("run-1", agentcontract.LLMCallRecord{
		Kind:     "text",
		Exchange: &model.WireExchange{Endpoint: "https://provider.example.test/v1/chat/completions", Request: `{"prompt":"hello"}`},
	})

	if len(llmCallRepository.records) != 1 || llmCallRepository.records[0].Exchange.Request != `{"prompt":"hello"}` {
		t.Fatalf("expected the exchange to reach the repository, got %+v", llmCallRepository.records)
	}
	if llmCallRepository.events[0].TaskRunID != "run-1" || llmCallRepository.events[0].Name != agentcontract.TaskEventLLMCall {
		t.Fatalf("expected the call event beside the exchange, got %+v", llmCallRepository.events[0])
	}
	if listed := taskEventService.ListTaskEvent("run-1"); len(listed) != 1 || listed[0].Name != agentcontract.TaskEventLLMCall {
		t.Fatalf("expected the call among the task's events, got %+v", listed)
	}
	if len(observed) != 1 || observed[0] != agentcontract.TaskEventLLMCall {
		t.Fatalf("expected observers to hear the call, got %v", observed)
	}
}
