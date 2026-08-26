package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	defaultCommandTimeout = 2 * time.Minute
	maximumCapturedOutput = 32 * 1024
)

type shellInput struct {
	Command       string `json:"command"`
	TimeoutSecond int    `json:"timeoutSecond"`
}

type shellOutput struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
	Completed  bool   `json:"completed"`
	OutputPath string `json:"outputPath,omitempty"`
}

var shellInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "minLength": 1},
    "timeoutSecond": {"type": "integer"}
  },
  "required": ["command"],
  "additionalProperties": false
}`)

var shellOutputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "exitCode": {"type": "integer"},
    "output": {"type": "string"},
    "truncated": {"type": "boolean"},
    "completed": {"type": "boolean"},
    "outputPath": {"type": "string"}
  },
  "required": ["exitCode", "output"],
  "additionalProperties": false
}`)

type shell struct {
	workingDirectoryPath string
	commandPrefix        []string
	interpreterName      string
}

func (runningShell shell) command(ctx context.Context, command string) *exec.Cmd {
	interpreter := runningShell.interpreter()
	if len(runningShell.commandPrefix) == 0 {
		shellCommand := exec.CommandContext(ctx, interpreter, "-c", command)
		shellCommand.Dir = runningShell.workingDirectoryPath
		return shellCommand
	}
	arguments := append(append([]string{}, runningShell.commandPrefix[1:]...), interpreter, "-c", command)
	return exec.CommandContext(ctx, runningShell.commandPrefix[0], arguments...)
}

func (runningShell shell) interpreter() string {
	if runningShell.interpreterName == "" {
		return "sh"
	}
	return runningShell.interpreterName
}

func (runningShell shell) withInterpreterFound(ctx context.Context) shell {
	probe := runningShell.command(ctx, "command -v bash")
	if probe.Run() != nil {
		return runningShell
	}
	runningShell.interpreterName = "bash"
	return runningShell
}

func (runningShell shell) resolvedWorkingDirectoryPath(ctx context.Context) string {
	if len(runningShell.commandPrefix) == 0 {
		return runningShell.workingDirectoryPath
	}
	capturedOutput := &bytes.Buffer{}
	command := runningShell.command(ctx, "pwd")
	command.Stdout = capturedOutput
	if errorValue := command.Run(); errorValue != nil {
		return runningShell.workingDirectoryPath
	}
	resolvedPath := strings.TrimSpace(capturedOutput.String())
	if resolvedPath == "" {
		return runningShell.workingDirectoryPath
	}
	return resolvedPath
}

func newWorkspaceToolSet(runningShell shell) *toolcontract.ToolSet {
	toolSet := toolcontract.NewToolSet([]string{
		toolcontract.ShellToolName,
		toolcontract.FileReadToolName,
		toolcontract.FileWriteToolName,
		toolcontract.FileEditToolName,
	})
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[shellInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/shell",
			Name:            toolcontract.ShellToolName,
			Description:     "Run one shell command in the working directory and read back its combined output and exit code. This is a full machine you control: a missing package is something to install and try again, not a reason the work cannot be done.",
			WhenToUse:       "anything the file tools do not cover: building, testing, searching, installing, inspecting the machine.",
			WhenNotToUse:    "reading or writing a file whose path you already have; file_read, file_write and file_edit do that with nothing to quote and nothing to escape.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     shellInputSchema,
			OutputSchema:    shellOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: shellOutputSchema},
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
		},
		Handler: func(toolContext context.Context, input shellInput) (toolcontract.ToolResult, error) {
			return runShellCommand(toolContext, runningShell, input), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
	registerFileTools(toolSet, runningShell)
	registerPlanTool(toolSet)
	registerImageTool(toolSet, runningShell)
	return toolSet
}

func runShellCommand(ctx context.Context, runningShell shell, input shellInput) toolcontract.ToolResult {
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "shell", "a command is required")
	}
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout(input.TimeoutSecond))
	defer cancel()

	capturedOutput := &bytes.Buffer{}
	shellCommand := runningShell.command(commandContext, command)
	shellCommand.Stdout = capturedOutput
	shellCommand.Stderr = capturedOutput
	runError := shellCommand.Run()

	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "shell",
			"the command was still running after "+commandTimeout(input.TimeoutSecond).String()+" and was stopped")
	}
	return shellResult(ctx, runningShell, shellCommand.ProcessState.ExitCode(), capturedOutput.String(), runError)
}

func shellResult(ctx context.Context, runningShell shell, exitCode int, output string, runError error) toolcontract.ToolResult {
	truncatedOutput, wasTruncated := truncateOutput(output)
	outputPath := ""
	if wasTruncated {
		outputPath = runningShell.spilledOutputPath(ctx, output)
	}
	document, marshalError := json.Marshal(shellOutput{ExitCode: exitCode, Output: truncatedOutput, Truncated: wasTruncated, Completed: exitCode == 0, OutputPath: outputPath})
	if marshalError != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "shell", marshalError.Error())
	}
	if runError != nil && exitCode == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "shell", runError.Error())
	}
	contentText := truncatedOutput
	if wasTruncated {
		contentText += truncationFooter(len(output), outputPath)
	}
	if exitCode != 0 {
		return exitedNonZeroResult(exitCode, contentText, document)
	}
	return toolcontract.ToolSuccessData(contentText, document)
}

func truncationFooter(totalBytes int, outputPath string) string {
	footer := "\n\n[output truncated: showing the last " + strconv.Itoa(maximumCapturedOutput) + " of " + strconv.Itoa(totalBytes) + " bytes"
	if outputPath != "" {
		footer += "; the full output is in " + outputPath
	}
	return footer + "]"
}

// The failure helpers put their summary where the output goes, and the output is what the model
// needs most here: the shell already said what went wrong, on the stream this captured.
func exitedNonZeroResult(exitCode int, output string, document json.RawMessage) toolcontract.ToolResult {
	result := toolcontract.ToolFailureWithOutput(
		toolcontract.FailureUnknown,
		toolcontract.FailureCodes.OperationFailed,
		"shell",
		"the command exited "+strconv.Itoa(exitCode),
		document,
	)
	result.Output.Content = output
	return result
}

func (runningShell shell) spilledOutputPath(ctx context.Context, output string) string {
	capturedPath := &bytes.Buffer{}
	command := runningShell.command(ctx, `mktemp "${TMPDIR:-/tmp}/terminal-output-XXXXXX"`)
	command.Stdout = capturedPath
	if command.Run() != nil {
		return ""
	}
	path := strings.TrimSpace(capturedPath.String())
	if path == "" || runningShell.writeFile(ctx, path, output) != nil {
		return ""
	}
	return path
}

func truncateOutput(output string) (string, bool) {
	if len(output) <= maximumCapturedOutput {
		return output, false
	}
	return strings.ToValidUTF8(output[len(output)-maximumCapturedOutput:], ""), true
}

func commandTimeout(timeoutSecond int) time.Duration {
	if timeoutSecond <= 0 {
		return defaultCommandTimeout
	}
	return time.Duration(timeoutSecond) * time.Second
}
