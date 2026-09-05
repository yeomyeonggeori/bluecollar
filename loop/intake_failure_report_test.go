package loop

import (
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestBuildIntakeFailureReportRecordsPlanAndObservedExecutionFactsSeparately(t *testing.T) {
	request := AgentRequest{Prompt: "사업 분류를 설정해줘", ResponseLanguage: "ko"}
	decision := IntakeDecision{
		Classification:  agentcontract.IntakeClassificationBoundedTask,
		TaskShape:       agentcontract.TaskShapeMaintenanceTask,
		Reason:          "업무 분류 변경 요청",
		UserFacingReply: "사업 현황을 조회하겠습니다.",
	}
	budget := turnBudgetContext{turnOptions: TurnOptions{MaxIterationCount: 20, MaxToolCallCount: 13, MaxElapsedSecond: 172}}

	report := buildIntakeFailureReport(budget, request, decision, "task-1")
	if report.IntakeFacts == nil {
		t.Fatal("expected typed intake facts")
	}
	if report.OriginalRequest != request.Prompt {
		t.Fatalf("expected original request to remain intact, got %+v", report)
	}
	if report.NextAction != "" || report.CompletedSummary != "" {
		t.Fatalf("expected no unverified next action or completed findings, got %+v", report)
	}
	if report.IntakeFacts.UsedExecutionIterations != 0 || report.IntakeFacts.UsedExecutionToolCalls != 0 {
		t.Fatalf("expected zero current-turn execution counts, got %+v", report.IntakeFacts)
	}
	if report.IntakeFacts.PlannedInterpretation != decision.Reason || report.IntakeFacts.UnverifiedUserFacingReply != decision.UserFacingReply {
		t.Fatalf("expected planned interpretation to remain explicitly labelled, got %+v", report.IntakeFacts)
	}
	if strings.Contains(report.RawError, "saved") || strings.Contains(report.RawError, "continuation") {
		t.Fatalf("expected neutral raw error, got %q", report.RawError)
	}
}
