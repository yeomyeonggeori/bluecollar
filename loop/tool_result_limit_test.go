package loop

import "testing"

func runnerWithContextWindow(contextWindowTokens int, toolResultMaxBytes int) *AgentTurnRunner {
	services := newTurnRunnerTestServices(nil, TurnOptions{
		ContextWindowTokens: contextWindowTokens,
		ToolResultMaxBytes:  toolResultMaxBytes,
	})
	return services.runner
}

func TestASmallContextGetsASmallerToolResult(t *testing.T) {
	roomy := runnerWithContextWindow(200000, 32768)
	cramped := runnerWithContextWindow(8000, 32768)

	if cramped.toolResultLimit() >= roomy.toolResultLimit() {
		t.Fatalf("an 8k model cannot be charged the same result as a 200k one, got %d against %d", cramped.toolResultLimit(), roomy.toolResultLimit())
	}
}

func TestTheLimitShrinksAsTheConversationFillsTheContext(t *testing.T) {
	runner := runnerWithContextWindow(100000, 32768)
	atStart := runner.toolResultLimit()

	runner.noteContextInUse(95000)

	if runner.toolResultLimit() >= atStart {
		t.Fatalf("a result has to fit in what is left, not in what there was, got %d against %d", runner.toolResultLimit(), atStart)
	}
}

func TestAnUnknownContextWindowFallsBackToTheConfiguredCeiling(t *testing.T) {
	runner := runnerWithContextWindow(0, 32768)

	if runner.toolResultLimit() != 32768 {
		t.Fatalf("with no context window reported the configured ceiling has to stand, got %d", runner.toolResultLimit())
	}
}

func TestAFullContextStillCutsTheResultDown(t *testing.T) {
	runner := runnerWithContextWindow(100000, 32768)
	runner.noteContextInUse(120000)

	limit := runner.toolResultLimit()

	if limit <= 0 {
		t.Fatalf("a limit of zero passes the whole result through untouched, which is the opposite of what a full context needs, got %d", limit)
	}
	if limit > maxSummaryTextLength {
		t.Fatalf("with the context already overrun the result has to come down to summary size, got %d", limit)
	}
}
