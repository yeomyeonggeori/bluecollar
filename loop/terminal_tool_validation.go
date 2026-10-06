package loop

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var agentActionsNoShellCanRun = []string{"set_quality_criteria", "reply"}

type terminalToolNameError struct {
	toolName string
}

func (errorValue terminalToolNameError) Error() string {
	return errorValue.toolName + " is an agent tool, not a shell command. Call it directly through the action schema."
}

func isTerminalToolNameError(errorValue error) bool {
	var typedError terminalToolNameError
	return errors.As(errorValue, &typedError)
}

func validateTerminalToolInput(toolName string, toolInput json.RawMessage, toolSets ...*toolcontract.ToolSet) error {
	if !isTerminalExecutionTool(toolName) {
		return nil
	}
	inputDocument, errorValue := parseToolInputDocument(toolName, toolInput)
	if errorValue != nil {
		return errorValue
	}
	command := strings.TrimSpace(stringValue(inputDocument["command"]))
	if command == "" {
		return nil
	}
	var toolSet *toolcontract.ToolSet
	if len(toolSets) > 0 {
		toolSet = toolSets[0]
	}
	commandToolName := firstTerminalCommandToken(command)
	isRegisteredTool := toolSet != nil && toolSet.IsRegistered(commandToolName)
	if isRegisteredTool || slices.Contains(agentActionsNoShellCanRun, commandToolName) {
		return terminalToolNameError{toolName: commandToolName}
	}
	return nil
}

func firstTerminalCommandToken(command string) string {
	for _, token := range terminalCommandTokens(command) {
		token = strings.Trim(token, `"'`)
		if strings.TrimSpace(token) != "" {
			return token
		}
	}
	return ""
}

func terminalCommandTokens(command string) []string {
	replacer := strings.NewReplacer(
		"\n", " ",
		";", " ",
		"&&", " ",
		"||", " ",
		"|", " ",
		"(", " ",
		")", " ",
		"=", " ",
		"<", " ",
		">", " ",
	)
	return strings.Fields(replacer.Replace(command))
}

func terminalRerunAfterWorkspaceMutation(actionDocument turnActionDocument, observations []turnObservation, duplicateObservation turnObservation) bool {
	if strings.TrimSpace(actionDocument.ToolName) != toolcontract.BashToolName {
		return false
	}
	seenDuplicateObservation := false
	for _, observation := range observations {
		if observation.ObservationID == duplicateObservation.ObservationID {
			seenDuplicateObservation = true
			continue
		}
		if !seenDuplicateObservation || observation.Failed() {
			continue
		}
		if isFileMutationTool(observation.Tool) {
			return true
		}
	}
	return false
}

func isTerminalExecutionTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case toolcontract.BashToolName:
		return true
	default:
		return false
	}
}
