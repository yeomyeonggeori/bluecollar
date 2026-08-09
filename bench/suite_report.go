package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type TaskResult struct {
	Suite   string     `json:"suite"`
	TaskID  string     `json:"taskID"`
	Metrics RunMetrics `json:"metrics"`
}

type SuiteReport struct {
	Suite   string       `json:"suite"`
	Harness string       `json:"harness"`
	Results []TaskResult `json:"results"`
}

type SuiteSummary struct {
	Suite   string `json:"suite"`
	Harness string `json:"harness"`
	Model   string `json:"model,omitempty"`

	Tasks    int     `json:"tasks"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"passRate"`

	MedianPromptTokensPerTurn float64 `json:"medianPromptTokensPerTurn"`
	MedianTurns               float64 `json:"medianTurns"`
	MedianToolCalls           float64 `json:"medianToolCalls"`
	MedianWallClockMS         float64 `json:"medianWallClockMs"`

	TotalPromptTokens     int64   `json:"totalPromptTokens"`
	TotalCompletionTokens int64   `json:"totalCompletionTokens"`
	TotalCostUSD          float64 `json:"totalCostUSD"`
	CostUSDPerPassedTask  float64 `json:"costUSDPerPassedTask"`
}

func (report SuiteReport) Summarize() SuiteSummary {
	summary := SuiteSummary{
		Suite:   report.Suite,
		Harness: report.Harness,
		Model:   report.measuredModel(),
		Tasks:   len(report.Results),
	}
	for _, result := range report.Results {
		if result.Metrics.Verdict == VerdictPassed {
			summary.Passed++
		}
		summary.TotalPromptTokens += result.Metrics.PromptTokens
		summary.TotalCompletionTokens += result.Metrics.CompletionTokens
		summary.TotalCostUSD += result.Metrics.CostUSD
	}
	summary.PassRate = ratio(summary.Passed, summary.Tasks)
	summary.CostUSDPerPassedTask = costPerPassedTask(summary.TotalCostUSD, summary.Passed)
	summary.MedianPromptTokensPerTurn = median(report.values(func(metrics RunMetrics) float64 { return metrics.PromptTokensPerTurn }))
	summary.MedianTurns = median(report.values(func(metrics RunMetrics) float64 { return float64(metrics.Turns) }))
	summary.MedianToolCalls = median(report.values(func(metrics RunMetrics) float64 { return float64(metrics.ToolCalls) }))
	summary.MedianWallClockMS = median(report.values(func(metrics RunMetrics) float64 { return float64(metrics.WallClockMS) }))
	return summary
}

func (report SuiteReport) measuredModel() string {
	for _, result := range report.Results {
		if result.Metrics.Model != "" {
			return result.Metrics.Model
		}
	}
	return ""
}

func (report SuiteReport) values(selectValue func(RunMetrics) float64) []float64 {
	values := make([]float64, 0, len(report.Results))
	for _, result := range report.Results {
		values = append(values, selectValue(result.Metrics))
	}
	return values
}

func ratio(part int, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func costPerPassedTask(totalCostUSD float64, passed int) float64 {
	if passed <= 0 {
		return 0
	}
	return totalCostUSD / float64(passed)
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64{}, values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

func WriteJSONReport(writer io.Writer, report SuiteReport) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func WriteComparison(writer io.Writer, summaries []SuiteSummary) error {
	header := fmt.Sprintf("%-22s %-26s %7s %9s %12s %7s %8s %11s\n",
		"harness", "suite", "tasks", "pass rate", "prompt/turn", "turns", "tools", "$/passed")
	if _, errorValue := io.WriteString(writer, header); errorValue != nil {
		return errorValue
	}
	if _, errorValue := io.WriteString(writer, strings.Repeat("-", len(header))+"\n"); errorValue != nil {
		return errorValue
	}
	for _, summary := range summaries {
		row := fmt.Sprintf("%-22s %-26s %7d %8.1f%% %12.0f %7.1f %8.1f %11.4f\n",
			summary.Harness, summary.Suite, summary.Tasks, summary.PassRate*100,
			summary.MedianPromptTokensPerTurn, summary.MedianTurns, summary.MedianToolCalls,
			summary.CostUSDPerPassedTask)
		if _, errorValue := io.WriteString(writer, row); errorValue != nil {
			return errorValue
		}
	}
	return nil
}
