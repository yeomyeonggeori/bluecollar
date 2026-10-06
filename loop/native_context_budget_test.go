package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func TestNativeToolResultsTriggerExistingContextPruning(t *testing.T) {
	options := TurnOptions{ContextWindowTokens: 100000}
	provider := &nativeAgentActionLanguageModel{}
	services := newTurnRunnerTestServices(provider, options)
	observations := numberedContextSummaryObservations(24, 18000, "catalog-row")
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "inspect the collection"}, Options: options, Observations: observations}
	visible := services.runner.promptStateForAction(context.Background(), "run-1", state).Observations
	if len(visible) != len(observations) {
		t.Fatalf("expected the same observation IDs, got %d of %d", len(visible), len(observations))
	}
	if len(visible[0].ContentText()) > taskContextPruneThresholdCharacters {
		t.Fatal("native raw tool results exceeded the context budget but only their short summaries were counted")
	}
	if visible[len(visible)-1].ContentText() != observations[len(observations)-1].ContentText() {
		t.Fatal("the newest result was changed before the model could act on it")
	}
	if len(observations[0].ContentText()) < 18000 {
		t.Fatal("prompt pruning changed the original observation")
	}
	if provider.chatCalls != 0 || provider.structuredCalls != 0 {
		t.Fatal("existing deterministic pruning added a model call")
	}
	before, isRepresentable := nativeAgentActionRequest(state)
	if !isRepresentable {
		t.Fatal("the test request has no native representation")
	}
	after, isRepresentable := nativeAgentActionRequest(withPromptObservations(state, visible))
	if !isRepresentable {
		t.Fatal("the pruned request lost its native representation")
	}
	encodedBefore, errorValue := json.Marshal(before)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	encodedAfter, errorValue := json.Marshal(after)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	t.Logf("serialized request bytes: before=%d after=%d; estimated tokens: old=%d before=%d after=%d", len(encodedBefore), len(encodedAfter), estimatePromptTokenCount(BuildAgentActionRequest(state).Messages), estimateChatCompletionTokenCount(before), estimateChatCompletionTokenCount(after))
}

func TestStructuredOnlyRequestsKeepTheirExistingContextBudget(t *testing.T) {
	options := TurnOptions{ContextWindowTokens: 100000}
	services := newTurnRunnerTestServices(&sequenceLanguageModel{}, options)
	observations := numberedContextSummaryObservations(24, 18000, "catalog-row")
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "inspect the collection"}, Options: options, Observations: observations}
	visible := services.runner.promptStateForAction(context.Background(), "run-1", state).Observations
	for index, observation := range observations {
		if visible[index].ContentText() != observation.ContentText() {
			t.Fatal("structured-only requests were pruned despite carrying only summaries")
		}
	}
}

func TestNativeContextPruningPreservesActiveFailureEvidence(t *testing.T) {
	options := TurnOptions{ContextWindowTokens: 100000}
	services := newTurnRunnerTestServices(&nativeAgentActionLanguageModel{}, options)
	observations := numberedContextSummaryObservations(24, 18000, "catalog-row")
	for index := range observations {
		observations[index].ToolIsReadOnly = true
	}
	observations[21].Failure = &toolcontract.ToolFailure{Kind: toolcontract.FailureExternalService, Code: toolcontract.FailureCodes.OperationFailed.String(), UserSafeSummary: "upstream service unavailable"}
	observations[21].ToolInputKey = "bash\x00{\"command\":\"read collection\"}"
	if _, hasFailureDebt := activeFailureDebt(observations); !hasFailureDebt {
		t.Fatal("the fixture does not represent an unresolved tool failure")
	}
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "inspect the collection"}, Options: options, Observations: observations}
	visible := services.runner.promptStateForAction(context.Background(), "run-1", state).Observations
	if len(visible[0].ContentText()) > taskContextPruneThresholdCharacters {
		t.Fatal("older unpinned results were not pruned")
	}
	for index := 21; index < len(observations); index++ {
		if visible[index].ContentText() != observations[index].ContentText() || visible[index].Failure != observations[index].Failure {
			t.Fatal("active failure evidence or its recovery observations were changed")
		}
	}
}

func TestNativeContextBudgetIncludesToolCallArguments(t *testing.T) {
	services := newTurnRunnerTestServices(&nativeAgentActionLanguageModel{}, TurnOptions{})
	state := nativeAgentActionTestState()
	state.Observations = []turnObservation{{ObservationID: "obs-001", Action: "continue", Tool: "write", ToolInput: json.RawMessage(`{"path":"output.txt","content":"` + strings.Repeat("x", 30000) + `"}`), Output: toolcontract.ToolOutput{Content: "saved"}}}
	if services.runner.estimateActionPromptTokenCount(state) < 7500 {
		t.Fatal("the source text in a prior write call was omitted from its request budget")
	}
}

func TestNativeRequestsWithinBudgetKeepTheirToolResults(t *testing.T) {
	options := TurnOptions{ContextWindowTokens: 100000}
	services := newTurnRunnerTestServices(&nativeAgentActionLanguageModel{}, options)
	observations := numberedContextSummaryObservations(12, 1000, "catalog-row")
	state := agentTaskState{Request: AgentTurnRequest{Prompt: "inspect the collection"}, Options: options, Observations: observations}
	visible := services.runner.promptStateForAction(context.Background(), "run-1", state).Observations
	if len(visible) != len(observations) {
		t.Fatal("a request within budget lost observations")
	}
	for index, observation := range observations {
		if visible[index].ContentText() != observation.ContentText() || visible[index].ObservationID != observation.ObservationID {
			t.Fatal("a request within budget changed tool result content or identity")
		}
	}
}

func BenchmarkActionContextBudget(b *testing.B) {
	services := newTurnRunnerTestServices(&nativeAgentActionLanguageModel{}, TurnOptions{})
	state := nativeAgentActionTestState()
	state.Observations = numberedContextSummaryObservations(24, 18000, "catalog-row")
	b.Run("previous-summary-estimate", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			estimatePromptTokenCount(BuildAgentActionRequest(state).Messages)
		}
	})
	b.Run("actual-native-request-estimate", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			services.runner.estimateActionPromptTokenCount(state)
		}
	})
}
