package loop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type recordingSpillStore struct {
	saved       []ToolResultSpill
	locator     string
	bytes       int
	hint        string
	failureText string
}

func (store *recordingSpillStore) SaveToolResultSpill(_ context.Context, spill ToolResultSpill) (ToolResultSpillRef, error) {
	store.saved = append(store.saved, spill)
	if store.failureText != "" {
		return ToolResultSpillRef{}, errors.New(store.failureText)
	}
	return ToolResultSpillRef{Locator: store.locator, Bytes: store.bytes, RetrievalHint: store.hint}, nil
}

func oversizedToolResult(runner *AgentTurnRunner) string {
	return strings.Repeat("build output line\n", runner.toolResultLimit())
}

func TestTheWholeOutputIsSavedWhereTheAgentCanReadItBack(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{textResponses: []string{"the build failed"}}, TurnOptions{})
	store := &recordingSpillStore{locator: "/workspace/private/people/p1/tmp/tasks/run-1/terminal_run.result.txt", bytes: 4096, hint: "Use grep or sed on that path."}
	services.runner.UseToolResultSpillStore(store)
	content := oversizedToolResult(services.runner)

	observation := services.runner.saveToolObservation(context.Background(), "run-1", "obs-1", "", "", "", "terminal_run", "tool-1",
		nil, "terminal_run", "terminal_run\x00{}", toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: content}},
		true, "/workspace", time.Time{}, 12)

	if len(store.saved) != 1 {
		t.Fatalf("an output the prompt cannot carry has to be saved somewhere, got %d saves", len(store.saved))
	}
	if store.saved[0].Content != content {
		t.Fatal("the point of saving is that the part the prompt dropped is still there, so it must be saved whole")
	}
	if !strings.Contains(observation.Summary, store.locator) {
		t.Fatalf("an agent cannot read a file it was never told about, got %q", observation.Summary)
	}
	if !strings.Contains(observation.Summary, store.hint) {
		t.Fatalf("the locator is useless without saying how to read it, got %q", observation.Summary)
	}
}

func TestASaveThatFailsLeavesTheCallSuccessful(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{textResponses: []string{"the build failed"}}, TurnOptions{})
	services.runner.UseToolResultSpillStore(&recordingSpillStore{failureText: "no space left on device"})
	content := oversizedToolResult(services.runner)

	observation := services.runner.saveToolObservation(context.Background(), "run-1", "obs-1", "", "", "", "terminal_run", "tool-1",
		nil, "terminal_run", "terminal_run\x00{}", toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: content}},
		true, "/workspace", time.Time{}, 12)

	if observation.Failed() {
		t.Fatal("a full disk on our side did not make the command the agent ran fail")
	}
	if !strings.Contains(observation.Summary, narrowTheOutputAdvice) {
		t.Fatalf("with nowhere to read the rest, the agent still needs to be told to ask more narrowly, got %q", observation.Summary)
	}
}

func TestAHostWithNoSpillStoreKeepsTheNarrowerAskAdvice(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{textResponses: []string{"the build failed"}}, TurnOptions{})
	content := oversizedToolResult(services.runner)

	observation := services.runner.saveToolObservation(context.Background(), "run-1", "obs-1", "", "", "", "terminal_run", "tool-1",
		nil, "terminal_run", "terminal_run\x00{}", toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: content}},
		true, "/workspace", time.Time{}, 12)

	if !strings.Contains(observation.Summary, narrowTheOutputAdvice) {
		t.Fatalf("a host that stores no spills must keep working exactly as before, got %q", observation.Summary)
	}
}

func TestAFailedSaveIsRecordedSoTheGapIsExplainable(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{textResponses: []string{"the build failed"}}, TurnOptions{})
	services.runner.UseToolResultSpillStore(&recordingSpillStore{failureText: "no space left on device"})

	services.runner.spillToolResult(context.Background(), "run-1", "obs-1", "terminal_run", "/workspace", "output")

	for _, event := range services.taskRunService.ListTaskEvent("run-1") {
		if event.Name == "tool.result_spill_failed" && strings.Contains(event.Body, "no space left on device") {
			return
		}
	}
	t.Fatal("an output that silently went missing is not diagnosable later")
}

func TestTheSavedObservationKeepsTheReasoningThatChoseIt(t *testing.T) {
	services := newTurnRunnerTestServices(nil, TurnOptions{})
	reasoning := "The contacts list has no venmo field, so I cross-reference the venmo accounts instead."

	observation := services.runner.saveToolObservation(context.Background(), "run-1", "obs-1", reasoning, "", "", "terminal_run", "tool-1",
		nil, "terminal_run", "terminal_run\x00{}", toolcontract.ToolResult{Output: toolcontract.ToolOutput{Content: "ok"}},
		true, "/workspace", time.Time{}, 12)

	if observation.AssistantText != reasoning {
		t.Fatalf("the next turn rebuilds its observations from the ledger, so reasoning set after the save is gone by the time the transcript is built: %q", observation.AssistantText)
	}
}
