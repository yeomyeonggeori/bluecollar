package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func eventBodiesNamed(events []taskstate.TaskEvent, name string) []string {
	bodies := []string{}
	for _, event := range events {
		if event.Name == name {
			bodies = append(bodies, event.Body)
		}
	}
	return bodies
}

func TestTheLedgerCarriesTheWordsARepeatReminderPutInFrontOfTheModel(t *testing.T) {
	services := newTurnRunnerTestServices(nil, TurnOptions{})
	state := &agentTaskState{}
	searchKey := "terminal_run\x00{\"command\":\"grep -r needle .\"}"
	for index := 1; index <= 3; index++ {
		observation := callObservation(nextObservationIDForObservations(state.Observations), "terminal_run", searchKey)
		services.runner.recordToolObservation("run-1", state, turnActionDocument{}, map[string]turnObservation{}, observation, "")
	}

	bodies := eventBodiesNamed(services.taskRunService.ListTaskEvent("run-1"), "agent.repeated_tool_call")
	if len(bodies) != 1 {
		t.Fatalf("the third identical call earns exactly one reminder, got %d events", len(bodies))
	}
	reminder := lastObservation(state.Observations)
	if strings.TrimSpace(reminder.ContentText()) == "" {
		t.Fatal("the reminder the model reads cannot be empty")
	}
	if !strings.Contains(bodies[0], reminder.ContentText()) {
		t.Fatalf("a reader replaying the record has to see what the model saw, got %q", bodies[0])
	}
}

func TestAdvisoryNoticesAreTurnLocalAndDoNotComeBackOnReplay(t *testing.T) {
	events := []taskstate.TaskEvent{
		{TaskEventID: "event-1", Name: "tool.note_write.result", Body: marshalEventBody(callObservation("obs-1", "note_write", "note_write\x00{}"))},
		{TaskEventID: "event-2", Name: "agent.repeated_tool_call", Body: marshalEventBody(reminderEventBody{Observation: newContentObservation("obs-2", "policy", "note_write", "stop repeating yourself")})},
		{TaskEventID: "event-3", Name: "agent.recovery_guidance", Body: marshalEventBody(newContentObservation("obs-3", "policy", "note_write", "try another route"))},
	}

	replayed := observationsFromTaskEvents(events)

	if len(replayed) != 1 || replayed[0].ObservationID != "obs-1" {
		t.Fatalf("replay rebuilds observations from tool results, so an advisory notice is turn-local by design; got %d observations", len(replayed))
	}
}

func lastObservation(observations []turnObservation) turnObservation {
	if len(observations) == 0 {
		return turnObservation{}
	}
	return observations[len(observations)-1]
}
