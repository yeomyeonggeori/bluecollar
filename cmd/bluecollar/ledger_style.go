package main

import (
	"os"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const (
	styleReset   = "\x1b[0m"
	styleDim     = "\x1b[2m"
	styleBold    = "\x1b[1m"
	styleRed     = "\x1b[31m"
	styleGreen   = "\x1b[32m"
	styleYellow  = "\x1b[33m"
	styleMagenta = "\x1b[35m"
	styleCyan    = "\x1b[36m"
)

func stderrWantsStyle() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("BLUECOLLAR_COLOR") == "always" {
		return true
	}
	fileInfo, errorValue := os.Stderr.Stat()
	return errorValue == nil && fileInfo.Mode()&os.ModeCharDevice != 0
}

func styledEventName(eventName string) string {
	if !stderrWantsStyle() {
		return eventName
	}
	return eventNameStyle(eventName) + eventName + styleReset
}

func eventNameStyle(eventName string) string {
	switch eventCategory(eventName) {
	case "task":
		return styleBold + styleCyan
	case "llm":
		return styleMagenta
	case "tool":
		return styleGreen
	case "completion_judge", "confirmation", "approval":
		return styleYellow
	default:
		return styleCyan
	}
}

func eventCategory(eventName string) string {
	category, _, hasDot := strings.Cut(eventName, ".")
	if !hasDot {
		return ""
	}
	return category
}

const terminalBodyLimit = 180

func clippedForTerminal(body string) string {
	if !stderrWantsStyle() {
		return body
	}
	runes := []rune(body)
	if len(runes) <= terminalBodyLimit {
		return body
	}
	return string(runes[:terminalBodyLimit]) + "…"
}

func styledEventBody(body string) string {
	if !stderrWantsStyle() {
		return body
	}
	return styleDim + body + styleReset
}

func styledStatus(status taskstate.TaskStatus) string {
	if !stderrWantsStyle() {
		return string(status)
	}
	if status == taskstate.TaskStatusCompleted {
		return styleBold + styleGreen + string(status) + styleReset
	}
	return styleBold + styleRed + string(status) + styleReset
}
