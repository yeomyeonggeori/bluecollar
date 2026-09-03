package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const (
	inkTask  = "\x1b[1;38;5;117m"
	inkModel = "\x1b[38;5;141m"
	inkTool  = "\x1b[38;5;84m"
	inkGate  = "\x1b[38;5;222m"
	inkAgent = "\x1b[38;5;110m"
	inkFault = "\x1b[38;5;210m"
	inkFaint = "\x1b[38;5;243m"
)

const ledgerLabelWidth = 12

func ledgerLine(ink string, glyph string, label string, value string) string {
	if glyph == "" {
		glyph = " "
	}
	padded := fmt.Sprintf("%-*s", ledgerLabelWidth, label)
	return ink + glyph + " " + padded + styleReset + " " + value
}

func prettyLedgerLine(taskEvent taskstate.TaskEvent) string {
	body := decodedEventBody(taskEvent.Body)
	switch taskEvent.Name {
	case taskstate.TaskEventTaskCreated:
		return ledgerLine(inkTask, "●", "task", quotedRequest(taskEvent.Body))
	case taskstate.TaskEventTaskRunning, taskstate.TaskEventAgentIntake, taskstate.TaskEventAgentGoalCompleted, taskstate.TaskEventAgentStepWorkingSet:
		return ""
	case taskstate.TaskEventAgentConversationBudget:
		return ledgerLine(inkFaint, "", "context", inkFaint+withThousands(body["contextWindowTokens"])+" tokens"+styleReset)
	case taskstate.TaskEventAgentInstructionsLoaded:
		return ledgerLine(inkFaint, "", "contract", inkFaint+clippedTo(goalInstruction(body), 96)+styleReset)
	case taskstate.TaskEventLLMCall:
		return ledgerLine(inkModel, "◆", "llm", llmCallSummary(body))
	case taskstate.TaskEventAgentAction:
		return ledgerLine(inkAgent, "◇", "action", actionSummary(body))
	case taskstate.TaskEventAgentExecutionState:
		return ledgerLine(inkFaint, "", "plan", inkFaint+clippedTo(stringField(body, "goal"), 96)+styleReset)
	case taskstate.TaskEventAgentEvidenceMissing:
		return ledgerLine(inkGate, "●", "gate", inkGate+"finish refused: evidence missing"+styleReset)
	case taskstate.TaskEventAgentCompletionRequired:
		output, _ := body["output"].(map[string]any)
		return ledgerLine(inkFaint, "", "gate", inkFaint+clippedTo(collapsedWhitespace(stringField(output, "content")), 96)+styleReset)
	case taskstate.TaskEventCompletionJudgeVerdict:
		return ledgerLine(inkGate, "●", "judge", judgeSummary(body))
	case taskstate.TaskEventCompletionJudgeDegraded:
		return ledgerLine(inkFaint, "", "judge", inkFaint+"unavailable, accepting the deterministic gate"+styleReset)
	case taskstate.TaskEventAgentBudgetExtendedOneLevel:
		return ledgerLine(inkFaint, "", "budget", inkFaint+"extended one level → "+stringField(body, "grantedLevel")+styleReset)
	case taskstate.TaskEventTaskCompleted:
		return ledgerLine(inkTool, "✓", "done", clippedTo(collapsedWhitespace(taskEvent.Body), 96))
	case taskstate.TaskEventTaskFailed:
		return ledgerLine(inkFault, "✗", "failed", clippedTo(collapsedWhitespace(taskEvent.Body), 96))
	}
	if toolName, isToolRequest := taskstate.ToolTaskEventToolName(taskEvent.Name, taskstate.ToolTaskEventRequestedSuffix); isToolRequest {
		return ledgerLine(inkTool, "▸", toolName, toolRequestSummary(body))
	}
	if _, isToolResult := taskstate.ToolTaskEventToolName(taskEvent.Name, taskstate.ToolTaskEventResultSuffix); isToolResult {
		return ledgerLine(inkFaint, "", "", toolResultSummary(body))
	}
	return ledgerLine(inkFaint, "", "", styledEventName(taskEvent.Name)+"  "+styledEventBody(clippedForTerminal(collapsedWhitespace(taskEvent.Body))))
}

func decodedEventBody(body string) map[string]any {
	document := map[string]any{}
	json.Unmarshal([]byte(body), &document)
	return document
}

func quotedRequest(body string) string {
	return "\x1b[1m" + clippedTo(collapsedWhitespace(body), 96) + styleReset
}

func goalInstruction(body map[string]any) string {
	activeGoal, isDocument := body["activeGoal"].(map[string]any)
	if !isDocument {
		return ""
	}
	return stringField(activeGoal, "originalInstruction")
}

func llmCallSummary(body map[string]any) string {
	parts := []string{stringField(body, "kind"), stringField(body, "model")}
	if latency, hasLatency := body["latencyMs"].(float64); hasLatency {
		parts = append(parts, fmt.Sprintf("%.1fs", latency/1000))
	}
	if prompt, hasPrompt := body["promptTokens"].(float64); hasPrompt {
		completion, _ := body["completionTokens"].(float64)
		parts = append(parts, fmt.Sprintf("%s → %s tok", withThousands(prompt), withThousands(completion)))
	}
	if errorText := stringField(body, "error"); errorText != "" {
		parts = append(parts, inkFault+clippedTo(errorText, 40)+styleReset)
	}
	return inkFaint + strings.Join(withoutEmptyStrings(parts), "  ·  ") + styleReset
}

func actionSummary(body map[string]any) string {
	action := stringField(body, "action")
	spoken := firstNonEmpty(stringField(body, "assistantText"), stringField(body, "message"))
	if toolName := stringField(body, "toolName"); toolName != "" && action == "continue" {
		action = action + " → " + toolName
	}
	summary := "\x1b[1m" + action + styleReset
	if spoken != "" {
		summary += "  " + inkFaint + "“" + clippedTo(spoken, 84) + "”" + styleReset
	}
	return summary
}

func judgeSummary(body map[string]any) string {
	if satisfied, _ := body["satisfied"].(bool); satisfied {
		return inkTool + "satisfied" + styleReset
	}
	return inkFault + "not satisfied" + styleReset + "  " + inkFaint + clippedTo(stringField(body, "reason"), 72) + styleReset
}

func toolRequestSummary(body map[string]any) string {
	input, isDocument := body["input"].(map[string]any)
	if !isDocument {
		return ""
	}
	if command := stringField(input, "command"); command != "" {
		return "\x1b[1m$ " + clippedTo(command, 88) + styleReset
	}
	compact, _ := json.Marshal(input)
	return inkFaint + clippedTo(string(compact), 88) + styleReset
}

func toolResultSummary(body map[string]any) string {
	output, _ := body["output"].(map[string]any)
	content := clippedTo(strings.Join(strings.Fields(stringField(output, "content")), " "), 88)
	if failure, isFailure := body["failure"].(map[string]any); isFailure && len(failure) > 0 {
		return inkFault + "→ failed" + styleReset + "  " + inkFaint + content + styleReset
	}
	return inkFaint + "→ " + content + styleReset
}

func stringField(document map[string]any, key string) string {
	value, _ := document[key].(string)
	return strings.TrimSpace(value)
}

func withThousands(value any) string {
	number, isNumber := value.(float64)
	if !isNumber {
		return ""
	}
	digits := fmt.Sprintf("%.0f", number)
	grouped := ""
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped += ","
		}
		grouped += string(digit)
	}
	return grouped
}

func withoutEmptyStrings(values []string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

func clippedTo(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
