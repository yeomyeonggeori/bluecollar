package agentcontract

import (
	"strings"
	"testing"
)

// The same heading stood over two different things: the conversation being
// continued, and other conversations that merely share the place. One of them
// answered a request with the other's subject.
func TestTheHeadingSaysWhichOfTheTwoThisIs(t *testing.T) {
	continuing := BuildVisibleContextDescription(VisibleContext{
		Messages: []VisibleContextMessage{{Speaker: "이동하", Text: "목요일 팁스 연구노트 작성 일정 추가해줘"}},
	}, "Asia/Seoul")
	others := BuildVisibleContextDescription(VisibleContext{
		MessagesOpenOtherExchanges: true,
		Messages:                   []VisibleContextMessage{{Speaker: "이동하", Text: "NVIDIA·젯슨 공급 미팅 지워주고"}},
	}, "Asia/Seoul")

	if strings.HasPrefix(others, strings.SplitN(continuing, "\n", 2)[0]) {
		t.Fatal("the two read the same, which is what let one be taken for the other")
	}
	if !strings.Contains(others, "may have nothing to do with it") {
		t.Fatalf("nothing tells the reader these are separate, got:\n%s", others)
	}
	if strings.Contains(continuing, "may have nothing to do with it") {
		t.Fatalf("this is the conversation being continued, got:\n%s", continuing)
	}
}
