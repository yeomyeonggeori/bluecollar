package loop

import (
	"context"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"strconv"
	"strings"
)

type ToolResultSpill struct {
	TaskRunID         string
	ObservationID     string
	ToolName          string
	WorkspaceRootPath string
	SuggestedName     string
	Content           string
}

// Locator is opaque: a host that stores spills somewhere other than a filesystem
// renders it as whatever its RetrievalHint tells the agent to use, so nothing parses it.
type ToolResultSpillRef struct {
	Locator       string
	Bytes         int
	RetrievalHint string
}

// A host that registers no store keeps the elided output and the advice to ask again
// more narrowly, so implementing this is optional.
type ToolResultSpillStore interface {
	SaveToolResultSpill(context.Context, ToolResultSpill) (ToolResultSpillRef, error)
}

func (spillRef ToolResultSpillRef) isUsable() bool {
	return strings.TrimSpace(spillRef.Locator) != ""
}

func spilledOutputAdvice(spillRef ToolResultSpillRef) string {
	advice := "The middle was elided here, but the whole output was saved at " + strings.TrimSpace(spillRef.Locator)
	if spillRef.Bytes > 0 {
		advice += " (" + strconv.Itoa(spillRef.Bytes) + " bytes)"
	}
	if hint := strings.TrimSpace(spillRef.RetrievalHint); hint != "" {
		advice += ". " + hint
	}
	return advice + " Read the part you need from there rather than running the command again."
}

func spillSuggestedName(toolName string) string {
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		trimmedToolName = "tool"
	}
	return trimmedToolName + ".result.txt"
}

// A storage failure of ours must not fail the agent's call, so every unhappy path
// here returns an empty ref and leaves the elided result standing.
func (agentTurnRunner *AgentTurnRunner) spillToolResult(ctx context.Context, taskRunID string, observationID string, toolName string, workspaceRootPath string, content string) ToolResultSpillRef {
	if agentTurnRunner.toolResultSpillStore == nil {
		return ToolResultSpillRef{}
	}
	spillRef, errorValue := agentTurnRunner.toolResultSpillStore.SaveToolResultSpill(ctx, ToolResultSpill{
		TaskRunID:         taskRunID,
		ObservationID:     observationID,
		ToolName:          strings.TrimSpace(toolName),
		WorkspaceRootPath: strings.TrimSpace(workspaceRootPath),
		SuggestedName:     spillSuggestedName(toolName),
		Content:           content,
	})
	if errorValue != nil {
		agentTurnRunner.appendEvent(taskRunID, taskstate.TaskEventToolResultSpillFailed, marshalEventBody(map[string]any{
			"observationID": observationID,
			"toolName":      strings.TrimSpace(toolName),
			"bytes":         len(content),
			"reason":        errorValue.Error(),
		}))
		return ToolResultSpillRef{}
	}
	if !spillRef.isUsable() {
		return ToolResultSpillRef{}
	}
	agentTurnRunner.appendEvent(taskRunID, taskstate.TaskEventToolResultSpilled, marshalEventBody(map[string]any{
		"observationID": observationID,
		"toolName":      strings.TrimSpace(toolName),
		"locator":       strings.TrimSpace(spillRef.Locator),
		"bytes":         spillRef.Bytes,
	}))
	return spillRef
}
