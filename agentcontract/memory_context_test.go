package agentcontract

import (
	"strings"
	"testing"
	"time"
)

func TestARememberedFactRendersAsItsStatementAndItsDate(t *testing.T) {
	rendered := BuildMemoryContext([]MemoryFact{
		{Content: "이샘플 keeps the quarterly ledger in Numbers", ValidAt: time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)},
		{Content: "이샘플 prefers reviews on Friday afternoons"},
	})
	expected := "What you already know:\n" +
		"- [2026-03-15] 이샘플 keeps the quarterly ledger in Numbers\n" +
		"- 이샘플 prefers reviews on Friday afternoons"
	if rendered != expected {
		t.Fatalf("expected:\n%s\n\ngot:\n%s", expected, rendered)
	}
}

// How a fact was found is not what it says. A score, an origin and a source
// kind in the prompt spend tokens on every turn and invite the model to
// reason about a ranking it cannot see the rest of.
func TestHowAFactWasFoundStaysOutOfThePrompt(t *testing.T) {
	rendered := BuildMemoryContext([]MemoryFact{{
		FactID:          "f17c2a",
		ScopeType:       MemoryScopeCircle,
		Content:         "이샘플 signs off on the quarterly close",
		Score:           0.87,
		SourceEpisodeID: "a3f9c2e1",
		SourceKind:      "fact",
	}})
	for _, leaked := range []string{"0.87", "score", "a3f9c2e1", "source", "kind", "f17c2a", MemoryScopeCircle} {
		if strings.Contains(rendered, leaked) {
			t.Errorf("expected %q to stay out of the prompt, got:\n%s", leaked, rendered)
		}
	}
}

func TestAnOverlongStatementIsCut(t *testing.T) {
	rendered := BuildMemoryContext([]MemoryFact{{Content: strings.Repeat("가", 300)}})
	if !strings.HasSuffix(rendered, "...") {
		t.Fatalf("expected an overlong statement to be cut, got %d characters", len([]rune(rendered)))
	}
}

func TestNothingRememberedRendersNothing(t *testing.T) {
	if rendered := BuildMemoryContext(nil); rendered != "" {
		t.Fatalf("expected no heading when there is nothing to say, got %q", rendered)
	}
	if rendered := BuildMemoryContext([]MemoryFact{{Content: "   "}}); rendered != "" {
		t.Fatalf("expected a blank statement to render nothing, got %q", rendered)
	}
}
