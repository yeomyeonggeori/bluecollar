package agentcontract

import (
	"strings"
	"testing"
)

// Several exchanges can share one place, and flattened into a list they read as
// one conversation. A request from one was answered with the subject of another,
// so each line says which exchange it belongs to and the reader decides what
// belongs together.
func TestEachLineSaysWhichExchangeItBelongsTo(t *testing.T) {
	description := BuildVisibleContextDescription(VisibleContext{Messages: []VisibleContextMessage{
		{Speaker: "이동하", Text: "edatec 미팅은 여명님도 참석함", ThreadRootID: "root-1"},
		{Speaker: "이동하", Text: "목요일 팁스 연구노트 작성 일정 추가해줘", ThreadRootID: "root-2"},
		{Speaker: "이동하", Text: "우경 나 여명 님 이렇게 세 명 참여해", ThreadRootID: "root-2"},
	}})

	for text, expected := range map[string]string{
		"edatec": "(exchange 1)",
		"팁스":     "(exchange 2)",
		"우경":     "(exchange 2)",
	} {
		line := lineHolding(description, text)
		if !strings.Contains(line, expected) {
			t.Fatalf("%q belongs to %s, got %q", text, expected, line)
		}
	}
}

// A place holding one exchange has nothing to tell apart, and a label there is
// noise the reader has to account for.
func TestOneExchangeCarriesNoLabel(t *testing.T) {
	description := BuildVisibleContextDescription(VisibleContext{Messages: []VisibleContextMessage{
		{Speaker: "이동하", Text: "해줘", ThreadRootID: "root-1"},
		{Speaker: "이동하", Text: "해", ThreadRootID: "root-1"},
	}})

	if strings.Contains(description, "exchange") {
		t.Fatalf("nothing here needs telling apart, got:\n%s", description)
	}
}

func lineHolding(description string, text string) string {
	for _, line := range strings.Split(description, "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}
