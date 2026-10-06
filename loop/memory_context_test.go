package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

// How a fact was found is not what it says. A score, an origin and a source
// kind in the prompt spend tokens on every turn and invite the model to
// reason about a ranking it cannot see the rest of.
func TestHowAFactWasFoundStaysOutOfThePrompt(t *testing.T) {
	rendered := agentcontract.BuildMemoryContext([]agentcontract.MemoryFact{{
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
