package loop

import (
	"encoding/json"
	"os"
	"testing"
)

func TestProtocolAgentActionFixturesMatchTurnActionDocument(t *testing.T) {
	documentByAction := protocolActionFixturesByAction(t)
	continueDocument, hasContinue := documentByAction["continue"]
	if !hasContinue {
		t.Fatal("expected a continue action fixture")
	}
	if continueDocument.ToolName != "file_read" || len(continueDocument.ToolInput) == 0 || continueDocument.ExecutionStateUpdate.Goal == "" {
		t.Fatalf("continue fixture lost required content: %#v", continueDocument)
	}
	replyDocument, hasReply := documentByAction["reply"]
	if !hasReply {
		t.Fatal("expected a reply action fixture")
	}
	if replyDocument.Message == "" || !replyDocument.ExpectsAnswer {
		t.Fatalf("reply fixture lost the question it asks: %#v", replyDocument)
	}
	if len(replyDocument.Choices) != 2 || len(replyDocument.Attachments) != 1 {
		t.Fatalf("reply fixture lost its choices or attachment: %#v", replyDocument)
	}
}

func protocolActionFixturesByAction(t *testing.T) map[string]turnActionDocument {
	t.Helper()
	documentByAction := map[string]turnActionDocument{}
	for _, fixture := range protocolAgentFixtures(t, "agent-action") {
		var document turnActionDocument
		if errorValue := json.Unmarshal(fixture, &document); errorValue != nil {
			t.Fatal(errorValue)
		}
		if _, isDuplicate := documentByAction[document.Action]; isDuplicate {
			t.Fatalf("expected one fixture per action, got a second %q", document.Action)
		}
		documentByAction[document.Action] = document
	}
	return documentByAction
}

func TestProtocolAgentMessageFixtureMatchesAgentMessage(t *testing.T) {
	var message AgentMessage
	if errorValue := json.Unmarshal(protocolAgentFixtures(t, "agent-message")[0], &message); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(message.Parts) != 2 || message.Parts[1].File == nil {
		t.Fatalf("unexpected agent message fixture: %#v", message)
	}
	if message.Parts[1].Source.MessageID != "message-1" {
		t.Fatalf("agent message fixture lost source identity: %#v", message.Parts[1].Source)
	}
}

func protocolAgentFixtures(t *testing.T, fixtureName string) []json.RawMessage {
	t.Helper()
	documentBytes, errorValue := os.ReadFile("testdata/protocol-fixtures.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var fixtures map[string][]json.RawMessage
	if errorValue := json.Unmarshal(documentBytes, &fixtures); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(fixtures[fixtureName]) == 0 {
		t.Fatalf("expected at least one %s fixture", fixtureName)
	}
	return fixtures[fixtureName]
}
