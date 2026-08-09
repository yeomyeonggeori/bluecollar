package bench

import (
	"bytes"
	"strings"
	"testing"
)

func resultWith(taskID string, verdict Verdict, promptTokensPerTurn float64, turns int, costUSD float64) TaskResult {
	return TaskResult{
		Suite:  "terminal-bench-2",
		TaskID: taskID,
		Metrics: RunMetrics{
			Verdict:             verdict,
			Turns:               turns,
			PromptTokensPerTurn: promptTokensPerTurn,
			PromptTokens:        int64(promptTokensPerTurn) * int64(turns),
			CostUSD:             costUSD,
		},
	}
}

func threeTaskReport() SuiteReport {
	return SuiteReport{
		Suite:   "terminal-bench-2",
		Harness: "bluecollar",
		Results: []TaskResult{
			resultWith("task-a", VerdictPassed, 10000, 4, 0.10),
			resultWith("task-b", VerdictFailed, 30000, 12, 0.60),
			resultWith("task-c", VerdictPassed, 20000, 6, 0.30),
		},
	}
}

func TestASummaryReportsWhatItCostToPassRatherThanWhatItCostToRun(t *testing.T) {
	summary := threeTaskReport().Summarize()

	if summary.Passed != 2 || summary.PassRate != 2.0/3.0 {
		t.Fatalf("expected two of three passed, got %+v", summary)
	}
	if summary.TotalCostUSD != 1.0 {
		t.Fatalf("expected the failed task's spend to count against the total, got %v", summary.TotalCostUSD)
	}
	if summary.CostUSDPerPassedTask != 0.5 {
		t.Fatalf("a harness that burns money failing is not cheap, expected 0.5, got %v", summary.CostUSDPerPassedTask)
	}
}

func TestTheMiddleTaskSetsTheReportedShapeRatherThanTheWorstOne(t *testing.T) {
	summary := threeTaskReport().Summarize()

	if summary.MedianPromptTokensPerTurn != 20000 {
		t.Fatalf("expected the median of 10000/20000/30000, got %v", summary.MedianPromptTokensPerTurn)
	}
	if summary.MedianTurns != 6 {
		t.Fatalf("expected the median of 4/6/12 turns, got %v", summary.MedianTurns)
	}
}

func TestAnEvenNumberOfTasksAveragesTheTwoInTheMiddle(t *testing.T) {
	report := threeTaskReport()
	report.Results = append(report.Results, resultWith("task-d", VerdictPassed, 40000, 8, 0.20))

	summary := report.Summarize()

	if summary.MedianPromptTokensPerTurn != 25000 {
		t.Fatalf("expected the mean of 20000 and 30000, got %v", summary.MedianPromptTokensPerTurn)
	}
}

func TestASuiteThatPassedNothingReportsNoCostPerPassRatherThanInfinity(t *testing.T) {
	report := SuiteReport{Suite: "terminal-bench-2", Harness: "bluecollar", Results: []TaskResult{
		resultWith("task-a", VerdictFailed, 10000, 4, 0.10),
	}}

	summary := report.Summarize()

	if summary.CostUSDPerPassedTask != 0 || summary.PassRate != 0 {
		t.Fatalf("expected no per-pass figure without a pass, got %+v", summary)
	}
}

func TestAnEmptySuiteSummarizesToZeroesRatherThanDividingByZero(t *testing.T) {
	summary := SuiteReport{Suite: "terminal-bench-2", Harness: "bluecollar"}.Summarize()

	if summary.Tasks != 0 || summary.PassRate != 0 || summary.MedianTurns != 0 {
		t.Fatalf("expected an empty summary, got %+v", summary)
	}
}

func TestAComparisonPutsEveryHarnessOnTheSameRow(t *testing.T) {
	buffer := &bytes.Buffer{}
	summaries := []SuiteSummary{
		threeTaskReport().Summarize(),
		{Suite: "terminal-bench-2", Harness: "pi", Tasks: 3, Passed: 3, PassRate: 1, MedianPromptTokensPerTurn: 6000, MedianTurns: 5, CostUSDPerPassedTask: 0.2},
	}

	if errorValue := WriteComparison(buffer, summaries); errorValue != nil {
		t.Fatal(errorValue)
	}

	rendered := buffer.String()
	for _, expected := range []string{"harness", "prompt/turn", "$/passed", "bluecollar", "pi", "20000", "6000"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected %q in the comparison, got:\n%s", expected, rendered)
		}
	}
}

func TestAReportRoundTripsAsJSONSoRunsCanBeComparedLater(t *testing.T) {
	buffer := &bytes.Buffer{}

	if errorValue := WriteJSONReport(buffer, threeTaskReport()); errorValue != nil {
		t.Fatal(errorValue)
	}

	for _, expected := range []string{`"suite": "terminal-bench-2"`, `"harness": "bluecollar"`, `"promptTokensPerTurn"`, `"verdict": "passed"`} {
		if !strings.Contains(buffer.String(), expected) {
			t.Fatalf("expected %q in the report, got:\n%s", expected, buffer.String())
		}
	}
}
