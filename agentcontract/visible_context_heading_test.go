package agentcontract

import (
	"strings"
	"testing"
)

func TestTheHeadingSaysWhichOfTheTwoThisIs(t *testing.T) {
	continuing := BuildVisibleContextDescription(VisibleContext{
		Messages: []VisibleContextMessage{{Speaker: "이샘플", Text: "목요일 주간 보고서 작성 일정 추가해줘"}},
	}, "Asia/Seoul")
	others := BuildVisibleContextDescription(VisibleContext{
		MessagesOpenOtherExchanges: true,
		Messages:                   []VisibleContextMessage{{Speaker: "이샘플", Text: "부산 공급사 미팅 지워주고"}},
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
