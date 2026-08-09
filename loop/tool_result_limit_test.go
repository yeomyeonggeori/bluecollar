package loop

import "testing"

func runnerWithContextWindow(contextWindowTokens int) *AgentTurnRunner {
	return newTurnRunnerTestServices(nil, TurnOptions{ContextWindowTokens: contextWindowTokens}).runner
}

func TestASmallContextGetsASmallerToolResult(t *testing.T) {
	roomy := runnerWithContextWindow(200000)
	cramped := runnerWithContextWindow(8000)

	if cramped.toolResultLimit() >= roomy.toolResultLimit() {
		t.Fatalf("an 8k model cannot be charged the same result as a 200k one, got %d against %d", cramped.toolResultLimit(), roomy.toolResultLimit())
	}
}

func TestTheLimitShrinksAsTheConversationFillsTheContext(t *testing.T) {
	runner := runnerWithContextWindow(100000)
	atStart := runner.toolResultLimit()

	runner.noteContextInUse(98000)

	if runner.toolResultLimit() >= atStart {
		t.Fatalf("a result has to fit in what is left, not in what there was, got %d against %d", runner.toolResultLimit(), atStart)
	}
}

func TestAnUnknownContextWindowUsesTheSameShareOfTheDefaultBudget(t *testing.T) {
	runner := runnerWithContextWindow(0)

	expected := defaultCompactionTriggerTokens * charactersPerToken / maxProgressObservations
	if runner.toolResultLimit() != expected {
		t.Fatalf("a model that reports no context still gets its share of the default conversation budget, got %d against %d", runner.toolResultLimit(), expected)
	}
}
