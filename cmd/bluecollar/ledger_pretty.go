package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
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

func prettyLedgerLine(taskEvent agentcontract.TaskEvent) string {
	body := decodedEventBody(taskEvent.Body)
	switch taskEvent.Name {
	case agentcontract.TaskEventTaskCreated:
		return ledgerLine(inkTask, "●", "task", quotedRequest(taskEvent.Body))
	case agentcontract.TaskEventTaskRunning, agentcontract.TaskEventAgentIntake, agentcontract.TaskEventAgentGoalCompleted, agentcontract.TaskEventAgentStepWorkingSet:
		return ""
	case agentcontract.TaskEventAgentConversationBudget:
		return ledgerLine(inkFaint, "", "context", inkFaint+withThousands(body["contextWindowTokens"])+" tokens"+styleReset)
	case agentcontract.TaskEventAgentInstructionsLoaded:
		return ledgerLine(inkFaint, "", "contract", inkFaint+clippedTo(goalInstruction(body), 96)+styleReset)
	case agentcontract.TaskEventLLMCall:
		return ledgerLine(inkModel, "◆", "llm", llmCallSummary(body))
	case agentcontract.TaskEventAgentAction:
		return ledgerLine(inkAgent, "◇", "action", actionSummary(body))
	case agentcontract.TaskEventAgentExecutionState:
		return ledgerLine(inkFaint, "", "plan", inkFaint+clippedTo(stringField(body, "goal"), 96)+styleReset)
	case agentcontract.TaskEventAgentEvidenceMissing:
		return ledgerLine(inkGate, "●", "gate", inkGate+"finish refused: evidence missing"+styleReset)
	case agentcontract.TaskEventAgentCompletionRequired:
		output, _ := body["output"].(map[string]any)
		return ledgerLine(inkFaint, "", "gate", inkFaint+clippedTo(collapsedWhitespace(stringField(output, "content")), 96)+styleReset)
	case agentcontract.TaskEventCompletionExpectedChanges:
		return ledgerLine(inkFaint, "", "expects", inkFaint+expectedChangesSummary(body)+styleReset)
	case agentcontract.TaskEventCompletionChangeCheck:
		return ledgerLine(inkGate, "●", "check", changeCheckSummary(body))
	case agentcontract.TaskEventCompletionCheckDegraded:
		return ledgerLine(inkFaint, "", "check", inkFaint+"unavailable, accepting the deterministic gate"+styleReset)
	case agentcontract.TaskEventAgentBudgetExtendedOneLevel:
		return ledgerLine(inkFaint, "", "budget", inkFaint+"extended one level → "+stringField(body, "grantedLevel")+styleReset)
	case agentcontract.TaskEventAgentPlanUpdated:
		return ledgerLine(inkFaint, "", "plan", inkFaint+planSummary(body)+styleReset)
	case agentcontract.TaskEventAgentFailureDebtCreated:
		return ledgerLine(inkFault, "", "debt", inkFaint+failureDebtSummary(body)+styleReset)
	case agentcontract.TaskEventAgentRecoveryGuidance:
		output, _ := body["output"].(map[string]any)
		return ledgerLine(inkFaint, "", "guidance", inkFaint+clippedTo(collapsedWhitespace(stringField(output, "content")), 96)+styleReset)
	case agentcontract.TaskEventAgentRecoveryAttempt:
		return ledgerLine(inkGate, "↻", "recover", inkFaint+recoveryAttemptSummary(body)+styleReset)
	case agentcontract.TaskEventAgentSteerReceived:
		return ledgerLine(inkGate, "↳", "steer", clippedTo(collapsedWhitespace(stringField(body, "instruction")), 88))
	case agentcontract.TaskEventTaskCompleted:
		return ledgerLine(inkTool, "✓", "done", clippedTo(collapsedWhitespace(taskEvent.Body), 96))
	case agentcontract.TaskEventTaskFailed:
		return ledgerLine(inkFault, "✗", "failed", clippedTo(collapsedWhitespace(taskEvent.Body), 96))
	}
	if toolName, isToolRequest := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventRequestedSuffix); isToolRequest {
		return ledgerLine(inkTool, "▸", toolName, toolRequestSummary(toolName, body))
	}
	if toolName, isToolResult := agentcontract.ToolTaskEventToolName(taskEvent.Name, agentcontract.ToolTaskEventResultSuffix); isToolResult {
		return ledgerLine(inkFaint, "", "", toolResultSummary(toolName, body))
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

func expectedChangesSummary(body map[string]any) string {
	changes, _ := body["expectedChanges"].([]any)
	if len(changes) == 0 {
		return "no change asked for"
	}
	sentences := []string{}
	for _, change := range changes {
		if sentence, isSentence := change.(string); isSentence {
			sentences = append(sentences, sentence)
		}
	}
	return clippedTo(strings.Join(sentences, ", "), 96)
}

func changeCheckSummary(body map[string]any) string {
	unmet, _ := body["unmet"].([]any)
	if len(unmet) == 0 {
		return inkTool + "every asked change is done" + styleReset
	}
	return inkFault + strconv.Itoa(len(unmet)) + " asked change(s) not done" + styleReset
}

func toolRequestSummary(toolName string, body map[string]any) string {
	input, isDocument := body["input"].(map[string]any)
	if !isDocument {
		return ""
	}
	if command := stringField(input, "command"); command != "" {
		return "\x1b[1m$ " + clippedTo(singleLineShellCommand(command), 88) + styleReset
	}
	if toolName == "plan" {
		if summary := planSummary(input); summary != "" {
			return clippedTo(summary, 92)
		}
	}
	if path := stringField(input, "path"); path != "" {
		detail := ""
		if toolName == "write" {
			if content, hasContent := input["content"].(string); hasContent {
				lineCount := strings.Count(content, "\n") + 1
				if strings.HasSuffix(content, "\n") && lineCount > 1 {
					lineCount--
				}
				detail = fmt.Sprintf("  %s(%d lines)%s", inkFaint, lineCount, styleReset)
			}
		} else if toolName == "file_read" {
			startLine, hasStart := input["startLine"].(float64)
			lineCount, hasCount := input["lineCount"].(float64)
			if hasStart && startLine > 0 {
				if hasCount && lineCount > 0 {
					detail = fmt.Sprintf("  %s(L%.0f-%.0f)%s", inkFaint, startLine, startLine+lineCount-1, styleReset)
				} else {
					detail = fmt.Sprintf("  %s(L%.0f+)%s", inkFaint, startLine, styleReset)
				}
			}
		}
		return "\x1b[1m" + clippedTo(path, 76) + styleReset + detail
	}
	compact, _ := json.Marshal(input)
	return inkFaint + clippedTo(string(compact), 88) + styleReset
}

func toolResultSummary(toolName string, body map[string]any) string {
	output, _ := body["output"].(map[string]any)
	rawContent := stringField(output, "content")
	if failure, isFailure := body["failure"].(map[string]any); isFailure && len(failure) > 0 {
		content := clippedTo(collapsedWhitespace(rawContent), 88)
		return inkFault + "→ failed" + styleReset + "  " + inkFaint + content + styleReset
	}
	if toolName == "plan" {
		decoded := decodedEventBody(rawContent)
		if stepSummary := planStepsSummary(decoded); stepSummary != "" {
			return inkFaint + "→ " + clippedTo(stepSummary, 88) + styleReset
		}
	}
	content := clippedTo(collapsedWhitespace(rawContent), 88)
	return inkFaint + "→ " + content + styleReset
}

func singleLineShellCommand(command string) string {
	lines := strings.Split(strings.ReplaceAll(command, "\r\n", "\n"), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts = append(parts, trimmed)
	}
	if len(parts) == 0 {
		return collapsedWhitespace(command)
	}
	return strings.Join(parts, "; ")
}

func planSummary(document map[string]any) string {
	goal := stringField(document, "goal")
	steps, _ := document["steps"].([]any)
	if goal == "" && len(steps) == 0 {
		return ""
	}
	if len(steps) > 0 {
		if goal == "" {
			return planStepsSummary(document)
		}
		return clippedTo(goal, 76) + "  " + inkFaint + fmt.Sprintf("(%d steps)", len(steps)) + styleReset
	}
	return clippedTo(goal, 88)
}

func planStepsSummary(document map[string]any) string {
	steps, _ := document["steps"].([]any)
	if len(steps) == 0 {
		return ""
	}
	titles := make([]string, 0, len(steps))
	for index, entry := range steps {
		stepMap, isMap := entry.(map[string]any)
		if !isMap {
			continue
		}
		title := stringField(stepMap, "title")
		if title == "" {
			continue
		}
		titles = append(titles, fmt.Sprintf("%d. %s", index+1, title))
	}
	return strings.Join(titles, "  ·  ")
}

func failureDebtSummary(body map[string]any) string {
	debt, _ := body["debt"].(map[string]any)
	latest, _ := debt["latestFailure"].(map[string]any)
	toolName := stringField(latest, "tool")
	if toolName == "" {
		toolName = "tool"
	}
	return "recorded failure debt for " + toolName
}

func recoveryAttemptSummary(body map[string]any) string {
	debt, _ := body["debt"].(map[string]any)
	latest, _ := debt["latestFailure"].(map[string]any)
	toolName := stringField(latest, "tool")
	nextTool := stringField(body, "tool")
	if toolName != "" && nextTool != "" && nextTool != toolName {
		return "recovering from " + toolName + " via " + nextTool
	}
	if toolName != "" {
		return "retrying after " + toolName + " failed"
	}
	return "recovering from failed tool call"
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
