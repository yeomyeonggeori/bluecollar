package loop

import (
	"context"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func terminalRunObservation(resultData string) turnObservation {
	return turnObservation{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          "terminal_run",
		ToolInput:     []byte(`{"command":"cli phone --help"}`),
		Output: toolcontract.ToolOutput{
			Content: "Commands: login search_contacts send_text_message",
			Data:    []byte(resultData),
		},
	}
}

func TestATerminalSummaryNeverReplacesOutputWithAnExitCode(t *testing.T) {
	observation := terminalRunObservation(`{"exitCode":0,"output":"Commands: login search_contacts send_text_message"}`)

	summary := summarizeTerminalRun(observation)

	if strings.Contains(summary, "exitCode") && !strings.Contains(summary, "search_contacts") {
		t.Fatalf("an agent shown only %q has never seen what the command printed", summary)
	}
}

func TestATerminalSummaryKeepsTheOutputItCanRead(t *testing.T) {
	observation := terminalRunObservation(`{"exitCode":0,"stdout":"Commands: login search_contacts send_text_message"}`)

	summary := summarizeTerminalRun(observation)

	if !strings.Contains(summary, "search_contacts") {
		t.Fatalf("a host that does report stdout must keep its tail in the summary, got %q", summary)
	}
}

func TestTheModelSeesWhatATerminalCommandPrinted(t *testing.T) {
	printed := "Usage: cli phone [OPTIONS]\nCommands:\n  login\n  search_contacts\n  send_text_message\n"
	observation := turnObservation{
		Tool: "terminal_run",
		Output: toolcontract.ToolOutput{
			Content: printed,
			Data:    []byte(`{"exitCode":0,"output":"Usage: cli phone [OPTIONS]","truncated":false,"completed":true}`),
		},
	}

	summary := modelVisibleToolResultSummary(context.Background(), nil, "terminal_run", observation)

	if !strings.Contains(summary, "search_contacts") {
		t.Fatalf("the agent runs commands to read their output; it was handed %q instead", summary)
	}
}
