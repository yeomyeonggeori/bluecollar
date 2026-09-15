package loop

import "testing"

func TestFailureReportRejectsUnrecordedEvidence(t *testing.T) {
	attempt := failureReportAttempt{ToolName: "record_create", InputSummary: `{"title":"sample"}`, ErrorCode: "operation_failed", FailureStage: "result_validation", Message: "The response could not be verified"}
	facts := failureReportFacts{Attempts: []failureReportAttempt{attempt}, BudgetState: "no_tool_fallback_available"}
	for _, field := range []string{"tool", "input", "code", "stage", "message", "budget", "duplicate", "extra"} {
		t.Run(field, func(t *testing.T) {
			used := failureReportFacts{Attempts: []failureReportAttempt{attempt}, BudgetState: facts.BudgetState}
			switch field {
			case "tool":
				used.Attempts[0].ToolName = "record_update"
			case "input":
				used.Attempts[0].InputSummary = `{"title":"changed"}`
			case "code":
				used.Attempts[0].ErrorCode = "unavailable"
			case "stage":
				used.Attempts[0].FailureStage = "retry"
			case "message":
				used.Attempts[0].Message = "A second call also failed"
			case "budget":
				used.BudgetState = "failure_report_required"
			case "duplicate":
				used.Attempts = append(used.Attempts, attempt)
			case "extra":
				used.Attempts = append(used.Attempts, failureReportAttempt{ToolName: "record_inspect"})
			}
			result := validateFailureReportAction(turnActionDocument{FailureResolution: failureResolutionFailureReport, UsedFailureFacts: used}, facts)
			if result.IsSatisfied {
				t.Fatal("accepted facts that do not match the recorded executions")
			}
		})
	}
}

func TestFailureReportAcceptsRepeatedCallsOnlyWhenRecorded(t *testing.T) {
	attempt := failureReportAttempt{ToolName: "record_create", InputSummary: "sample", ErrorCode: "operation_failed", FailureStage: "transport", Message: "Connection closed"}
	facts := failureReportFacts{Attempts: []failureReportAttempt{attempt, attempt}, BudgetState: "failure_report_required"}
	result := validateFailureReportAction(turnActionDocument{FailureResolution: failureResolutionFailureReport, UsedFailureFacts: facts}, facts)
	if !result.IsSatisfied {
		t.Fatal(result.Message)
	}
}
