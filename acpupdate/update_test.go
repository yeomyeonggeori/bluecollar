package acpupdate

import (
	"encoding/json"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

func TestAToolRequestBecomesAToolCallTitledByWhatItWasPointedAt(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		body     string
		expected string
	}{
		{"a path is what a file tool is about", `{"observationID":"o1","input":{"path":"/workspace/notes.md"}}`, "file_read(/workspace/notes.md)"},
		{"a command is what the terminal is about", `{"observationID":"o1","input":{"command":"bun test"}}`, "file_read(bun test)"},
		{"the first named field wins", `{"observationID":"o1","input":{"query":"q","path":"/a"}}`, "file_read(/a)"},
		{"a call with nothing worth showing is still named", `{"observationID":"o1","input":{"limit":5}}`, "file_read"},
		{"a field that is not text is ignored", `{"observationID":"o1","input":{"path":3,"query":"q"}}`, "file_read(q)"},
		{"newlines do not break the title", `{"observationID":"o1","input":{"command":"a\nb"}}`, "file_read(a b)"},
		{"a long subject is cut", `{"observationID":"o1","input":{"path":"` + strings.Repeat("x", 60) + `"}}`, "file_read(" + strings.Repeat("x", 48) + "…)"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			update, isToolCall := ToolCallForEvent(taskstate.RawTurnEvent{Name: "tool.file_read.requested", Body: testCase.body})
			if !isToolCall || update.ToolCall == nil {
				t.Fatalf("a tool request was not a tool call: %+v", update)
			}
			if update.ToolCall.Title != testCase.expected {
				t.Fatalf("title = %q, want %q", update.ToolCall.Title, testCase.expected)
			}
		})
	}
}

func TestAToolRequestCarriesItsCallIDStatusAndLedgerRecord(t *testing.T) {
	update, _ := ToolCallForEvent(taskstate.RawTurnEvent{Name: "tool.file_read.requested", Body: `{"observationID":"o1","input":{"path":"/a"}}`})

	toolCall := update.ToolCall
	if toolCall.ToolCallId != "o1" || toolCall.Status != acp.ToolCallStatusPending {
		t.Fatalf("tool call = %+v, want id o1 pending", toolCall)
	}
	record, isRecorded := toolCall.Meta[agentcontract.LedgerMetaKey].(agentcontract.LedgerRecord)
	if !isRecorded || record.Name != "tool.file_read.requested" {
		t.Fatalf("meta = %+v, want the ledger record of the request", toolCall.Meta)
	}
}

func TestAToolResultBecomesAnUpdateThatSaysWhetherItFailed(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		body     string
		expected acp.ToolCallStatus
	}{
		{"a result without failure completed", `{"observationID":"o1"}`, acp.ToolCallStatusCompleted},
		{"a result with a failure failed", `{"observationID":"o1","failure":{"code":"operation_failed"}}`, acp.ToolCallStatusFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			update, isToolCall := ToolCallForEvent(taskstate.RawTurnEvent{Name: "tool.file_read.result", Body: testCase.body})
			if !isToolCall || update.ToolCallUpdate == nil {
				t.Fatalf("a tool result was not a tool call update: %+v", update)
			}
			if update.ToolCallUpdate.ToolCallId != "o1" || *update.ToolCallUpdate.Status != testCase.expected {
				t.Fatalf("update = %+v, want o1 %s", update.ToolCallUpdate, testCase.expected)
			}
		})
	}
}

func TestAnEventThatIsNotAToolCallIsOnlyAThoughtForTheLedger(t *testing.T) {
	rawTurnEvent := taskstate.RawTurnEvent{Name: "agent.checkpoint.sent", Body: `{}`}

	if _, isToolCall := ToolCallForEvent(rawTurnEvent); isToolCall {
		t.Fatal("a checkpoint was taken for a tool call")
	}
	if update := ForEvent(rawTurnEvent); update.AgentThoughtChunk == nil {
		t.Fatalf("a checkpoint became %+v, want a thought chunk", update)
	}
}

func TestALedgerRecordKeepsTheBodyExactlyAsTheLoopWroteItAcrossTheWire(t *testing.T) {
	body := `{"unmet":[],"carriedOut":{"expected0":0.1}}`
	update := ForEvent(taskstate.RawTurnEvent{Name: "completion.change_check", Body: body})
	sent, errorValue := json.Marshal(update.AgentThoughtChunk.Meta)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	received := map[string]any{}
	if errorValue := json.Unmarshal(sent, &received); errorValue != nil {
		t.Fatal(errorValue)
	}
	again, _ := json.Marshal(received[agentcontract.LedgerMetaKey])
	record := agentcontract.LedgerRecord{}
	if errorValue := json.Unmarshal(again, &record); errorValue != nil {
		t.Fatal(errorValue)
	}

	if record.EventBody() != body {
		t.Fatalf("a host that mirrors the ledger gets keys in the order the loop wrote them only from the text, got %q", record.EventBody())
	}
}
