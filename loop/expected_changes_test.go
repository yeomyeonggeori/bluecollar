package loop

import (
	"context"
	"slices"
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

func clockOutCorrectionAnsweredAfterADay() AgentTurnRequest {
	seoul, _ := time.LoadLocation("Asia/Seoul")
	return AgentTurnRequest{
		Company:       agentcontract.CompanyContext{TimeZone: "Asia/Seoul"},
		ActiveGoal:    agentcontract.ActiveGoal{OriginalInstruction: "퇴근시간 지금으로 바꿔줘"},
		Prompt:        "해",
		ToolSet:       taskDeleteToolSet(),
		TurnStartedAt: time.Date(2026, 11, 27, 8, 6, 0, 0, seoul),
	}
}

func TestATurnResumedOnAnAnswerKeepsTheChangesItsRequestWasGiven(t *testing.T) {
	asked := "요청 시점의 현재 시각으로 퇴근 기록 수정"
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument(asked), expectedChangesDocument("퇴근 기록을 현재 시각(08:07)으로 수정")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := clockOutCorrectionAnsweredAfterADay()
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	firstTurn := expectedChangesOfOneTurn(services, taskRun.TaskRunID, request)
	request.TurnStartedAt = request.TurnStartedAt.Add(time.Minute)
	resumedTurn := expectedChangesOfOneTurn(services, taskRun.TaskRunID, request)

	if len(languageModel.requests) != 1 {
		t.Fatalf("expected the request's changes defined once across its pause, got %d definitions", len(languageModel.requests))
	}
	if !slices.Equal(firstTurn, []string{asked}) || !slices.Equal(resumedTurn, firstTurn) {
		t.Fatalf("expected the resumed turn held to what the request asked, got %q then %q", firstTurn, resumedTurn)
	}
}

func TestALaterMessageAboutTheRequestDefinesItsChangesAgain(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument("04:30으로 퇴근 기록 수정"), expectedChangesDocument("08:00으로 퇴근 기록 수정")}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	request := clockOutCorrectionAnsweredAfterADay()
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	expectedChangesOfOneTurn(services, taskRun.TaskRunID, request)
	request.Prompt = "아니 8시로"
	corrected := expectedChangesOfOneTurn(services, taskRun.TaskRunID, request)

	if len(languageModel.requests) != 2 || !slices.Equal(corrected, []string{"08:00으로 퇴근 기록 수정"}) {
		t.Fatalf("expected a correction to the request read again, got %d definitions and %q", len(languageModel.requests), corrected)
	}
}

func TestAnInstructionCarriedFromEarlierIsNotDatedWithThisTurn(t *testing.T) {
	text := expectedChangesRequestText(clockOutCorrectionAnsweredAfterADay())

	if !strings.Contains(text, "Request:\n퇴근시간 지금으로 바꿔줘") || !strings.Contains(text, "Latest message about it (sent 2026-11-27 08:06):\n해") {
		t.Fatalf("expected only the latest message dated with this turn:\n%s", text)
	}
}

func expectedChangesOfOneTurn(services turnRunnerTestServices, taskRunID string, request AgentTurnRequest) []string {
	services.runner.beginExpectedChanges(context.Background(), taskRunID, request)
	defer services.runner.forgetExpectedChanges(taskRunID)
	changes, _ := services.runner.expectedChangesFor(context.Background(), taskRunID, request)
	return changes
}
