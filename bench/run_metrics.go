package bench

import (
	"encoding/json"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type Verdict string

const (
	VerdictUnverified Verdict = "unverified"
	VerdictPassed     Verdict = "passed"
	VerdictFailed     Verdict = "failed"
)

type RunMetrics struct {
	TaskRunID string  `json:"taskRunID,omitempty"`
	Model     string  `json:"model,omitempty"`
	Verdict   Verdict `json:"verdict"`

	TerminalStatus string `json:"terminalStatus,omitempty"`
	ReachedEnd     bool   `json:"reachedEnd"`

	ContextWindowTokens int  `json:"contextWindowTokens"`
	WindowWasDeclared   bool `json:"windowWasDeclared"`

	Turns              int `json:"turns"`
	ToolCalls          int `json:"toolCalls"`
	FailedToolCalls    int `json:"failedToolCalls"`
	ApprovalHolds      int `json:"approvalHolds"`
	RecoveryAttempts   int `json:"recoveryAttempts"`
	LanguageModelCalls int `json:"languageModelCalls"`

	PromptBytes        int64 `json:"promptBytes"`
	PromptTokens       int64 `json:"promptTokens"`
	CompletionTokens   int64 `json:"completionTokens"`
	CachedPromptTokens int64 `json:"cachedPromptTokens"`
	ReasoningTokens    int64 `json:"reasoningTokens"`
	TotalTokens        int64 `json:"totalTokens"`

	PromptBytesPerTurn  float64 `json:"promptBytesPerTurn"`
	PromptTokensPerTurn float64 `json:"promptTokensPerTurn"`
	CostUSD             float64 `json:"costUSD"`
	ModelLatencyMS      int64   `json:"modelLatencyMs"`
	WallClockMS         int64   `json:"wallClockMs"`
}

var terminalTaskEventStatuses = map[string]string{
	agentcontract.TaskEventTaskCompleted: string(agentcontract.TaskStatusCompleted),
	agentcontract.TaskEventTaskFailed:    string(agentcontract.TaskStatusFailed),
	agentcontract.TaskEventTaskCancelled: string(agentcontract.TaskStatusCancelled),
	agentcontract.TaskEventTaskBlocked:   string(agentcontract.TaskStatusBlocked),
}

func MeasureTaskRun(taskRunID string, taskEvents []agentcontract.TaskEvent) RunMetrics {
	metrics := RunMetrics{TaskRunID: taskRunID, Verdict: VerdictUnverified}
	for _, taskEvent := range taskEvents {
		countTaskEvent(&metrics, taskEvent)
	}
	metrics.WallClockMS = elapsedMilliseconds(taskEvents)
	metrics.PromptBytesPerTurn = perTurn(metrics.PromptBytes, metrics.Turns)
	metrics.PromptTokensPerTurn = perTurn(metrics.PromptTokens, metrics.Turns)
	return metrics
}

func countTaskEvent(metrics *RunMetrics, taskEvent agentcontract.TaskEvent) {
	if terminalStatus, isTerminal := terminalTaskEventStatuses[taskEvent.Name]; isTerminal {
		metrics.TerminalStatus = terminalStatus
		metrics.ReachedEnd = true
		return
	}
	switch {
	case taskEvent.Name == agentcontract.TaskEventAgentAction:
		metrics.Turns++
	case taskEvent.Name == agentcontract.TaskEventApprovalPendingCall:
		metrics.ApprovalHolds++
	case taskEvent.Name == agentcontract.TaskEventAgentRecoveryAttempt:
		metrics.RecoveryAttempts++
	case taskEvent.Name == agentcontract.TaskEventLLMCall:
		countLanguageModelCall(metrics, taskEvent.Body)
	case taskEvent.Name == agentcontract.TaskEventAgentConversationBudget:
		readConversationBudget(metrics, taskEvent.Body)
	case isToolRequestedEvent(taskEvent.Name):
		metrics.ToolCalls++
	case isToolResultEvent(taskEvent.Name):
		countToolResult(metrics, taskEvent.Body)
	}
}

func readConversationBudget(metrics *RunMetrics, body string) {
	budget := struct {
		ContextWindowTokens int  `json:"contextWindowTokens"`
		WindowWasDeclared   bool `json:"windowWasDeclared"`
	}{}
	if json.Unmarshal([]byte(body), &budget) != nil {
		return
	}
	metrics.ContextWindowTokens = budget.ContextWindowTokens
	metrics.WindowWasDeclared = budget.WindowWasDeclared
}

func countLanguageModelCall(metrics *RunMetrics, body string) {
	record := agentcontract.LLMCallRecord{}
	if json.Unmarshal([]byte(body), &record) != nil {
		return
	}
	metrics.LanguageModelCalls++
	metrics.PromptBytes += int64(record.PromptBytes)
	metrics.PromptTokens += record.PromptTokens
	metrics.CompletionTokens += record.CompletionTokens
	metrics.CachedPromptTokens += record.CachedPromptTokens
	metrics.ReasoningTokens += record.ReasoningTokens
	metrics.TotalTokens += record.TotalTokens
	metrics.CostUSD += firstNonZeroCost(record.CostUSD, record.UpstreamInferenceCost)
	metrics.ModelLatencyMS += record.LatencyMS
	if metrics.Model == "" {
		metrics.Model = record.Model
	}
}

func countToolResult(metrics *RunMetrics, body string) {
	observation := struct {
		Failure *struct {
			RequiresApproval bool `json:"requiresApproval"`
		} `json:"failure"`
	}{}
	if json.Unmarshal([]byte(body), &observation) != nil || observation.Failure == nil {
		return
	}
	if observation.Failure.RequiresApproval {
		return
	}
	metrics.FailedToolCalls++
}

func isToolRequestedEvent(eventName string) bool {
	_, isToolRequest := agentcontract.ToolTaskEventToolName(eventName, agentcontract.ToolTaskEventRequestedSuffix)
	return isToolRequest
}

func isToolResultEvent(eventName string) bool {
	_, isToolResult := agentcontract.ToolTaskEventToolName(eventName, agentcontract.ToolTaskEventResultSuffix)
	return isToolResult
}

func firstNonZeroCost(candidates ...float64) float64 {
	for _, candidate := range candidates {
		if candidate != 0 {
			return candidate
		}
	}
	return 0
}

func perTurn(total int64, turns int) float64 {
	if turns <= 0 {
		return 0
	}
	return float64(total) / float64(turns)
}

func elapsedMilliseconds(taskEvents []agentcontract.TaskEvent) int64 {
	if len(taskEvents) < 2 {
		return 0
	}
	earliest, latest := taskEvents[0].CreatedAt, taskEvents[0].CreatedAt
	for _, taskEvent := range taskEvents {
		if taskEvent.CreatedAt.Before(earliest) {
			earliest = taskEvent.CreatedAt
		}
		if taskEvent.CreatedAt.After(latest) {
			latest = taskEvent.CreatedAt
		}
	}
	return latest.Sub(earliest).Milliseconds()
}

func (metrics RunMetrics) WithVerdict(verdict Verdict) RunMetrics {
	metrics.Verdict = verdict
	return metrics
}
