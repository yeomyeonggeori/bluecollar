package loop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestCompanyContextRendersIdentityAndSelfUpdateRule(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		Company: CompanyContext{
			Name:           "주식회사 예시상사",
			BrandName:      "예시인턴",
			Slogan:         "모두에게 예시 인턴을",
			Description:    "예시 서비스를 만드는 테스트 회사",
			Representative: "최예제",
			Website:        "https://example.com",
		},
	})
	for _, expected := range []string{
		"Our company:",
		"주식회사 예시상사 (brand: 예시인턴) — 모두에게 예시 인턴을",
		"represented by 최예제",
		"company_info_get",
		"company_metric_record",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("company context missing %q in:\n%s", expected, contextText)
		}
	}
}

func TestCompanyContextEmptyStateSaysSoAndAsksRatherThanInventing(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{})
	for _, expected := range []string{"Our company:", "Not registered yet", "ask once"} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("empty company state missing %q in:\n%s", expected, contextText)
		}
	}
}

func TestCompanyContextNamesNoToolThatDoesNotExist(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{})
	for _, absent := range []string{"company_info_set", "company_metric_record", "company.record.add"} {
		if strings.Contains(contextText, absent) {
			t.Fatalf("no such tool is registered anywhere, so every call carried an instruction the model could not follow: %q", absent)
		}
	}
}

func TestATurnReadsTheClockInTheZoneItsRequestCarries(t *testing.T) {
	agentKernel, taskRunService := newKernelTestServices()
	agentKernel.UseLanguageModelProvider(&recoveryChatNoticeProvider{chatReply: "지금은 처리하지 못했습니다."})
	decision := TurnDecision{
		Route: TurnRouteStartTask, Classification: IntakeClassificationBoundedTask,
		TaskShape: TaskShapeMaintenanceTask, TaskLevel: TaskLevelLow, ResponseLanguage: "ko",
	}
	request := AgentTurnRequest{
		RequesterIdentity: RequesterIdentity{RequesterPersonID: "person-kernel-test"},
		ConversationID:    "conversation-kernel-test",
		Prompt:            "재택 출근 어제 21:30에 해서 지금 퇴근으로 찍어줘",
		Company:           CompanyContext{Name: "주식회사 예시상사", TimeZone: "Asia/Seoul"},
		TurnStartedAt:     time.Date(2026, 11, 5, 15, 32, 0, 0, time.UTC),
		ToolSet:           taskDeleteToolSet(),
	}

	result, _ := agentKernel.RunPlannedTurn(context.Background(), request, turnclassification.Routing{Decision: &decision})

	taskEvents := taskRunService.ListTaskEvent(result.TaskRun.TaskRunID)
	if !taskEventsContain(taskEvents, string(agentcontract.TaskEventAgentIntake), "") {
		t.Fatal("expected the turn to reach the task it starts")
	}
	if taskEventsContain(taskEvents, string(agentcontract.TaskEventAgentCompanyTimeZoneFallback), "") {
		t.Fatal("the turn dropped the company its request carried and read the clock in a guessed zone")
	}
}
