package taskstate

import (
	"strings"
	"sync"
	"testing"
)

func TestTaskRunObserverConcurrentAppendUnregisterNoPanic(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		taskEventService := NewTaskEventService()
		events := make(chan struct{}, 16)
		var appendGroup sync.WaitGroup
		appendGroup.Add(1)
		go func() {
			defer appendGroup.Done()
			for appendIndex := 0; appendIndex < 200; appendIndex++ {
				taskEventService.AppendTaskEvent("run-1", "tool.x.result", "{}")
			}
		}()
		unregister := taskEventService.RegisterTaskRunObserver("run-1", func(RawTurnEvent) {
			select {
			case events <- struct{}{}:
			default:
			}
		})
		unregister()
		close(events)
		appendGroup.Wait()
	}
}

func TestRegisterTurnObserverGlobalReceivesUntilUnregister(t *testing.T) {
	taskEventService := NewTaskEventService()
	received := []string{}
	unregister := taskEventService.RegisterTurnObserver(func(rawTurnEvent RawTurnEvent) {
		received = append(received, rawTurnEvent.Name)
	})
	taskEventService.AppendTaskEvent("run-1", "tool.x.requested", "{}")
	taskEventService.AppendTaskEvent("run-2", "tool.y.requested", "{}")
	unregister()
	taskEventService.AppendTaskEvent("run-3", "tool.z.requested", "{}")
	if len(received) != 2 || received[0] != "tool.x.requested" || received[1] != "tool.y.requested" {
		t.Fatalf("expected events from any task run before unregister only, got %v", received)
	}
}

func TestTaskRunObserverWithoutRegistrationPersistsIdentically(t *testing.T) {
	taskEventService := NewTaskEventService()
	taskEventService.AppendTaskEvent("run-1", "tool.x.result", "body")
	stored := taskEventService.ListTaskEvent("run-1")
	if len(stored) != 1 {
		t.Fatalf("expected one persisted event, got %d", len(stored))
	}
	if stored[0].Name != "tool.x.result" || stored[0].Body != "body" {
		t.Fatalf("expected persisted event unchanged, got %+v", stored[0])
	}
}

func TestACrashingObserverDoesNotTakeTheAppendDownWithIt(t *testing.T) {
	taskEventService := NewTaskEventService()
	reachedAfterTheCrash := false
	defer taskEventService.RegisterTurnObserver(func(RawTurnEvent) {
		panic("the host's observer had a bad day")
	})()
	defer taskEventService.RegisterTurnObserver(func(RawTurnEvent) {
		reachedAfterTheCrash = true
	})()

	taskEventService.AppendTaskEvent("run-1", "tool.x.result", "{}")

	if !reachedAfterTheCrash {
		t.Fatal("one bad subscriber starved the observers queued behind it")
	}
	stored := taskEventService.ListTaskEvent("run-1")
	names := []string{}
	for _, taskEvent := range stored {
		names = append(names, taskEvent.Name)
	}
	if len(stored) != 2 || names[0] != "tool.x.result" || names[1] != "task.observer_crashed" {
		t.Fatalf("a swallowed crash is a crash nobody can diagnose; the ledger has to say it happened: %v", names)
	}
	if !strings.Contains(stored[1].Body, "the host's observer had a bad day") {
		t.Fatalf("expected the recorded crash to carry what the observer panicked with, got %q", stored[1].Body)
	}
}
