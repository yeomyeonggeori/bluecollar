package loop

import (
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func buildIntakeFailureReport(turnBudget turnBudgetContext, request AgentRequest, intakeDecision IntakeDecision, taskRunID string) agentcontract.FailureReport {
	elapsedSecond := 0.0
	if !turnBudget.turnStartedAt.IsZero() {
		elapsedSecond = time.Since(turnBudget.turnStartedAt).Seconds()
	}
	carriedOutToolNames := make([]string, 0, len(request.CarriedOutCalls))
	for _, carriedOutCall := range request.CarriedOutCalls {
		carriedOutToolNames = append(carriedOutToolNames, carriedOutCall.ToolName)
	}
	return agentcontract.BuildIntakeFailureReport(agentcontract.IntakeFailureReportInput{
		OriginalRequest:           request.Prompt,
		ResponseLanguage:          request.ResponseLanguage,
		DiagnosticEventID:         taskRunID + ":intake_limit",
		PlannedInterpretation:     intakeDecision.Reason,
		UnverifiedUserFacingReply: intakeDecision.UserFacingReply,
		Classification:            intakeDecision.Classification,
		TaskShape:                 intakeDecision.TaskShape,
		MaxIterationCount:         turnBudget.turnOptions.MaxIterationCount,
		MaxToolCallCount:          turnBudget.turnOptions.MaxToolCallCount,
		MaxElapsedSecond:          turnBudget.turnOptions.MaxElapsedSecond,
		ElapsedSecond:             elapsedSecond,
		CarriedOutToolNames:       carriedOutToolNames,
		PriorTaskID:               request.PriorTask.TaskRunID,
		PriorTaskStatus:           request.PriorTask.Status,
		PriorTaskResult:           request.PriorTask.Result,
		PriorTaskFailureReason:    request.PriorTask.FailureReason,
	})
}
