package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	commandNotFoundExitCode = 127
	defaultCommandTimeout   = 2 * time.Minute
	maximumCapturedOutput   = 32 * 1024
)

type terminalRunInput struct {
	Command          string `json:"command"`
	TimeoutSecond    int    `json:"timeoutSecond"`
	ApprovalRequired bool   `json:"approvalRequired"`
	ApprovalReason   string `json:"approvalReason"`
}

type terminalRunOutput struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
	Completed  bool   `json:"completed"`
	OutputPath string `json:"outputPath,omitempty"`
}

var terminalRunInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "minLength": 1},
    "timeoutSecond": {"type": "integer"},
    "approvalRequired": {"type": "boolean"},
    "approvalReason": {"type": "string"}
  },
  "required": ["command"],
  "additionalProperties": false
}`)

var terminalRunOutputSchema = json.RawMessage(`{
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
		toolcontract.TerminalRunToolName,
		toolcontract.FileReadToolName,
		toolcontract.FileWriteToolName,
		toolcontract.FileEditToolName,
	})
	toolcontract.RegisterToolFunction(toolSet, toolcontract.ToolFunction[terminalRunInput, toolcontract.ToolResult]{
		Definition: toolcontract.ToolDefinition{
			ID:              "bluecollar/terminal_run",
			Name:            toolcontract.TerminalRunToolName,
			Description:     "Run one shell command in the working directory and read back its combined output and exit code. This is a full machine you control: a missing package is something to install and try again, not a reason the work cannot be done. There is no separate approver here — the person who started this runner is the one who asked — so approvalRequired changes nothing and never blocks a command.",
			Visibility:      toolcontract.ToolVisibilityModel,
			InputSchema:     terminalRunInputSchema,
			OutputSchema:    terminalRunOutputSchema,
			ResultContract:  &toolcontract.ToolResultContract{Schema: terminalRunOutputSchema},
			SideEffectClass: toolcontract.ToolSideEffectStateChange,
		},
		Handler: func(toolContext context.Context, input terminalRunInput) (toolcontract.ToolResult, error) {
			return runShellCommand(toolContext, runningShell, input), nil
		},
		Result: toolcontract.IdentityToolResult,
	})
	registerFileTools(toolSet, runningShell)
	registerPlanTool(toolSet)
	registerImageTool(toolSet, runningShell)
	return toolSet
}

func runShellCommand(ctx context.Context, runningShell shell, input terminalRunInput) toolcontract.ToolResult {
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return toolcontract.ToolFailureResult(toolcontract.FailureInvalidInput, toolcontract.FailureCodes.InvalidInput, "terminal_run", "a command is required")
	}
	commandContext, cancel := context.WithTimeout(ctx, commandTimeout(input.TimeoutSecond))
	defer cancel()

	capturedOutput := &bytes.Buffer{}
	shellCommand := runningShell.command(commandContext, command)
	shellCommand.Stdout = capturedOutput
	shellCommand.Stderr = capturedOutput
	runError := shellCommand.Run()

	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return toolcontract.ToolFailureResult(toolcontract.FailureDependencyUnavailable, toolcontract.FailureCodes.Unavailable, "terminal_run",
			"the command was still running after "+commandTimeout(input.TimeoutSecond).String()+" and was stopped")
	}
	exitCode := shellCommand.ProcessState.ExitCode()
	if exitCode == commandNotFoundExitCode {
		return toolcontract.ToolFailureResult(toolcontract.FailureNotFound, toolcontract.FailureCodes.ToolNameInShell, "terminal_run",
			"the shell could not find "+firstWordOf(command)+"; this tool runs shell commands, so send something a shell can run")
	}
	return terminalRunResult(ctx, runningShell, exitCode, capturedOutput.String(), runError)
}

func firstWordOf(command string) string {
	firstWord, _, _ := strings.Cut(strings.TrimSpace(command), " ")
	return firstWord
}

func terminalRunResult(ctx context.Context, runningShell shell, exitCode int, output string, runError error) toolcontract.ToolResult {
	truncatedOutput, wasTruncated := truncateOutput(output)
	outputPath := ""
	if wasTruncated {
		outputPath = runningShell.spilledOutputPath(ctx, output)
	}
	document, marshalError := json.Marshal(terminalRunOutput{ExitCode: exitCode, Output: truncatedOutput, Truncated: wasTruncated, Completed: exitCode == 0, OutputPath: outputPath})
	if marshalError != nil {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "terminal_run", marshalError.Error())
	}
	if runError != nil && exitCode == 0 {
		return toolcontract.ToolFailureResult(toolcontract.FailureUnknown, toolcontract.FailureCodes.OperationFailed, "terminal_run", runError.Error())
	}
	return toolcontract.ToolSuccessData(truncatedOutput, document)
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
