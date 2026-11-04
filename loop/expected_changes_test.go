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
		Company:       agentcontract.CompanyContext{TimeZone: "Asia/Seoul"},
		Prompt:        "재택 출근 아홉시 그리고 지금 퇴근",
		TurnStartedAt: time.Date(2026, 10, 30, 10, 0, 0, 0, seoul),
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
		Prompt: "업무로 추가해줘",
		VisibleContext: agentcontract.VisibleContext{Messages: []agentcontract.VisibleContextMessage{
			{Speaker: "이샘플", Text: "다음 주 화요일에 제안서 리뷰해야 해"},
		}},
	}

	text := expectedChangesRequestText(request)

	if !strings.Contains(text, "\n이샘플: 다음 주") || !strings.Contains(text, "Request:\n업무로 추가해줘") {
		t.Fatalf("a message without a time should render without a stamp:\n%s", text)
	}
}
