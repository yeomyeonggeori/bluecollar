package loop

import (
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestExpectedChangesRequestTextDatesEveryMessage(t *testing.T) {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	request := AgentTurnRequest{
		Company:        agentcontract.CompanyContext{TimeZone: "Asia/Seoul"},
		Prompt:         "재택 출근 아홉시 그리고 지금 퇴근",
		OriginIsThread: true,
		TurnStartedAt:  time.Date(2026, 10, 30, 10, 0, 0, 0, seoul),
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "이샘플", Text: "아침 9시 30분 재택 출근 10시 4분 퇴근도", SentAt: time.Date(2026, 10, 29, 10, 49, 0, 0, seoul)},
		}},
	}

	text := expectedChangesRequestText(request)

	for _, wanted := range []string{"[2026-10-29 10:49] 이샘플: 아침 9시 30분", "Request (sent 2026-10-30 10:00):\n재택 출근 아홉시"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("expected changes request text lacks %q:\n%s", wanted, text)
		}
	}
}

func TestExpectedChangesRequestTextOmitsAnUnknownTime(t *testing.T) {
	request := AgentTurnRequest{
		Prompt:         "업무로 추가해줘",
		OriginIsThread: true,
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "이샘플", Text: "다음 주 화요일에 제안서 리뷰해야 해"},
		}},
	}

	text := expectedChangesRequestText(request)

	if !strings.Contains(text, "\n이샘플: 다음 주") || !strings.Contains(text, "Request:\n업무로 추가해줘") {
		t.Fatalf("a message without a time should render without a stamp:\n%s", text)
	}
}

func dataRoomExchangeBeforeTheWeeklyReport() agentcontract.VisibleContext {
	return agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
		{Speaker: "이샘플", Text: "데이터룸에 투자 계약서 올려줘"},
		{Speaker: "김인턴", Text: "어느 폴더에 올릴까요?"},
	}}
}

func TestARequestThatOpensItsOwnThreadIsJudgedWithoutTheExchangesBeforeIt(t *testing.T) {
	request := AgentTurnRequest{Prompt: "이번 주 주간보고 써줘", VisibleContext: dataRoomExchangeBeforeTheWeeklyReport()}

	text := expectedChangesRequestText(request)
	asked := askedState(request, []string{"주간보고를 작성한다"})

	if strings.Contains(text, "데이터룸") || strings.Contains(text, "Conversation before the request") {
		t.Fatalf("a request that opens its own thread was judged with another thread's unfinished request in front of it:\n%s", text)
	}
	if _, hasConversation := asked["conversationBefore"]; hasConversation {
		t.Fatalf("the change check read another thread as the conversation before the request: %v", asked["conversationBefore"])
	}
}

func TestARequestInsideAThreadIsJudgedWithThatThread(t *testing.T) {
	request := AgentTurnRequest{Prompt: "투자 폴더", OriginIsThread: true, VisibleContext: dataRoomExchangeBeforeTheWeeklyReport()}

	text := expectedChangesRequestText(request)
	asked := askedState(request, []string{"투자 계약서를 데이터룸 투자 폴더에 올린다"})

	if !strings.Contains(text, "이샘플: 데이터룸에 투자 계약서 올려줘") {
		t.Fatalf("an answer inside a thread means nothing without the thread it answers:\n%s", text)
	}
	if _, hasConversation := asked["conversationBefore"]; !hasConversation {
		t.Fatal("the change check lost the thread the answer belongs to")
	}
}
