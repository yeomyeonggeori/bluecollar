package loop

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"testing"
)

func TestToolNamesMatchRequiresExactCanonicalIdentity(t *testing.T) {
	if !toolcontract.ToolNamesMatch(" file_deliver ", toolcontract.FileDeliverToolName) {
		t.Fatal("expected surrounding whitespace to be ignored")
	}
	for _, legacyToolName := range []string{"ask_choice", "artifact.deliver", "file.attach", "terminal.session"} {
		if toolcontract.ToolNamesMatch(legacyToolName, normalizePersistedToolName(legacyToolName)) {
			t.Fatalf("expected legacy tool %q not to match its canonical replacement", legacyToolName)
		}
	}
}

func TestEffectiveObservationToolNamePreservesDirectToolNames(t *testing.T) {
	if got := effectiveObservationToolName("task_add", json.RawMessage(`{"title":"t1"}`)); got != "task_add" {
		t.Fatalf("expected direct tool name unchanged, got %q", got)
	}
	if got := effectiveObservationToolName(toolcontract.BashToolName, json.RawMessage(`{"command":"ls"}`)); got != toolcontract.BashToolName {
		t.Fatalf("expected terminal tool name unchanged, got %q", got)
	}
}
