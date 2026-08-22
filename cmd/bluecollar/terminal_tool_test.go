package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func invokeTerminalRun(t *testing.T, workspacePath string, input string) toolcontract.ToolResult {
	t.Helper()
	return invokeTerminalRunThrough(t, shell{workingDirectoryPath: workspacePath}, input)
}

func invokeTerminalRunThrough(t *testing.T, runningShell shell, input string) toolcontract.ToolResult {
	t.Helper()
	toolSet := newWorkspaceToolSet(runningShell)
	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.TerminalRunToolName,
		Input:    json.RawMessage(input),
	})
	if errorValue != nil {
		t.Fatalf("expected the invocation to return a result: %v", errorValue)
	}
	return result
}

func decodedOutput(t *testing.T, result toolcontract.ToolResult) terminalRunOutput {
	t.Helper()
	output := terminalRunOutput{}
	if errorValue := json.Unmarshal(result.Output.Data, &output); errorValue != nil {
		t.Fatalf("expected structured output, got %q: %v", result.Output.Data, errorValue)
	}
	return output
}

func TestTheAgentSeesWhatTheCommandPrinted(t *testing.T) {
	result := invokeTerminalRun(t, t.TempDir(), `{"command":"echo hello from the shell"}`)

	if result.Failed() {
		t.Fatalf("expected a successful command, got %+v", result)
	}
	output := decodedOutput(t, result)
	if !strings.Contains(output.Output, "hello from the shell") || output.ExitCode != 0 {
		t.Fatalf("expected the printed line and a zero exit, got %+v", output)
	}
}

func TestACommandRunsWhereTheWorkIs(t *testing.T) {
	workspacePath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "marker.txt"), []byte("found"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}

	result := invokeTerminalRun(t, workspacePath, `{"command":"cat marker.txt"}`)

	if !strings.Contains(decodedOutput(t, result).Output, "found") {
		t.Fatalf("a shell that starts somewhere else cannot do the task it was given, got %+v", result)
	}
}

func TestAFailingCommandReportsItsExitCodeRatherThanVanishing(t *testing.T) {
	result := invokeTerminalRun(t, t.TempDir(), `{"command":"echo to stderr 1>&2; exit 3"}`)

	output := decodedOutput(t, result)
	if output.ExitCode != 3 {
		t.Fatalf("expected the exit code the agent has to react to, got %+v", output)
	}
	if !strings.Contains(output.Output, "to stderr") {
		t.Fatalf("expected stderr captured alongside stdout, got %+v", output)
	}
}

func TestACommandThatNeverEndsIsStoppedAndSaidSo(t *testing.T) {
	result := invokeTerminalRun(t, t.TempDir(), `{"command":"sleep 30","timeoutSecond":1}`)

	if !result.Failed() {
		t.Fatalf("expected the hung command to come back as a failure, got %+v", result)
	}
	if !strings.Contains(result.UserSafeFailureSummary(), "still running") {
		t.Fatalf("expected the agent to learn why it stopped, got %q", result.UserSafeFailureSummary())
	}
}

func TestAnEmptyCommandIsRefusedBeforeAShellIsStarted(t *testing.T) {
	result := invokeTerminalRun(t, t.TempDir(), `{"command":"   "}`)

	if !result.Failed() || result.FailureCode() != toolcontract.FailureCodes.InvalidInput.String() {
		t.Fatalf("expected an invalid input failure, got %+v", result)
	}
}

func TestALongOutputKeepsItsEndBecauseThatIsWhereTheErrorIs(t *testing.T) {
	result := invokeTerminalRun(t, t.TempDir(), `{"command":"head -c 40000 /dev/zero | tr '\\0' 'a'; echo THEEND"}`)

	output := decodedOutput(t, result)
	if !output.Truncated {
		t.Fatalf("expected the capture to report that it dropped the head, got %+v", output.Truncated)
	}
	if !strings.Contains(output.Output, "THEEND") {
		t.Fatal("truncating the tail throws away the failure the agent has to read")
	}
	if len(output.Output) > maximumCapturedOutput {
		t.Fatalf("expected the capture bounded, got %d bytes", len(output.Output))
	}
}

func TestARunnerToldToBringNoShellBringsNone(t *testing.T) {
	if turnToolSet(runOptions{withoutTools: true}, shell{}) != nil {
		t.Fatal("expected no tool set when the runner is asked to answer from reasoning alone")
	}
	if turnToolSet(runOptions{}, shell{workingDirectoryPath: t.TempDir()}) == nil {
		t.Fatal("expected a shell by default, because a runner with no tools cannot be benchmarked")
	}
}

func TestAWrappedShellRunsTheCommandThroughTheSandboxItWasGiven(t *testing.T) {
	result := invokeTerminalRunThrough(t, shell{commandPrefix: []string{"env", "BLUECOLLAR_SANDBOX=yes"}}, `{"command":"echo $BLUECOLLAR_SANDBOX"}`)

	if result.Failed() {
		t.Fatalf("expected the wrapped command to run, got %+v", result)
	}
	if !strings.Contains(decodedOutput(t, result).Output, "yes") {
		t.Fatalf("a prefix that is not actually in front of the command sends the agent to the wrong machine, got %+v", result)
	}
}

func TestAClassifierCannotTalkTheRunnerOutOfTheTaskItWasGiven(t *testing.T) {
	refused := agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteGiveUp,
		Classification:   agentcontract.IntakeClassificationUnsupported,
		TaskShape:        agentcontract.TaskShapeImmediateReply,
		InitialToolNames: []string{toolcontract.TerminalRunToolName},
	}

	started := startingTheTaskItWasGiven(refused)

	if started.Route != agentcontract.TurnRouteStartTask || started.Classification != agentcontract.IntakeClassificationBoundedTask {
		t.Fatalf("an argv is not an ambiguous message, expected the task to start, got %+v", started)
	}
	if len(started.InitialToolNames) != 1 {
		t.Fatal("what the classifier worked out about the task is what gives the completion gate its teeth, and it must survive")
	}
}

func TestAClassifierThatAlreadyWantsToStartIsLeftAlone(t *testing.T) {
	planned := agentcontract.TurnDecision{
		Route:          agentcontract.TurnRouteStartTask,
		Classification: agentcontract.IntakeClassificationBoundedTask,
		TaskShape:      agentcontract.TaskShapeResearchTask,
	}

	if startingTheTaskItWasGiven(planned).TaskShape != agentcontract.TaskShapeResearchTask {
		t.Fatal("expected a usable decision to pass through untouched")
	}
}

func TestAMissingCommandIsNamedByTheShellRatherThanGuessedFromTheFirstWord(t *testing.T) {
	result := invokeTerminalRun(t, t.TempDir(), `{"command":"cd . \u0026\u0026 definitely-not-installed --version"}`)

	output := decodedOutput(t, result)
	if output.ExitCode != 127 {
		t.Fatalf("expected the shell's own exit code, got %+v", output)
	}
	if !strings.Contains(output.Output, "definitely-not-installed") {
		t.Fatalf("the shell said which command was missing and the model has to see it, got %q", output.Output)
	}
	if strings.Contains(output.Output, "could not find cd") {
		t.Fatalf("cd is a builtin that cannot be missing; blaming the first word sends the model after the wrong thing, got %q", output.Output)
	}
}

func TestTruncatedOutputStaysDecodableWhenTheCutLandsMidCharacter(t *testing.T) {
	output := strings.Repeat("가", maximumCapturedOutput)

	truncatedOutput, wasTruncated := truncateOutput(output)

	if !wasTruncated {
		t.Fatal("expected an output this long to be truncated")
	}
	if !utf8.ValidString(truncatedOutput) {
		t.Fatal("a cut that splits a character hands the caller bytes it cannot decode, and the whole run's measurement is lost to it")
	}
}

func TestEveryWorkspaceToolReachesTheModel(t *testing.T) {
	toolSet := newWorkspaceToolSet(shell{workingDirectoryPath: t.TempDir()})

	for _, toolName := range []string{
		toolcontract.TerminalRunToolName,
		toolcontract.FileReadToolName,
		toolcontract.FileWriteToolName,
		toolcontract.FileEditToolName,
	} {
		if !toolSet.CanExpose(toolName) {
			t.Fatalf("%s registers without error and then never reaches the model, which reads as the model choosing not to use it", toolName)
		}
	}
}

func TestFileEditReplacesOnlyThePassageItWasGiven(t *testing.T) {
	workspacePath := t.TempDir()
	filePath := filepath.Join(workspacePath, "program.py")
	if errorValue := os.WriteFile(filePath, []byte("def f(n):\n    return n + 1\n"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}
	runningShell := shell{workingDirectoryPath: workspacePath}

	result := editFileThroughShell(context.Background(), runningShell, fileEditInput{
		Path: "program.py", FindText: "return n + 1", ReplaceText: "return n - 1",
	})

	if result.Failure != nil {
		t.Fatalf("expected the edit to apply, got %+v", result.Failure)
	}
	edited, _ := os.ReadFile(filePath)
	if string(edited) != "def f(n):\n    return n - 1\n" {
		t.Fatalf("expected only the named passage to change, got %q", string(edited))
	}
	if len(result.Effects) != 1 || result.Effects[0].Path != "program.py" {
		t.Fatalf("an edit that records no effect leaves the completion gate guessing whether work happened, got %+v", result.Effects)
	}
}

func TestFileEditRefusesAPassageItCannotPlace(t *testing.T) {
	workspacePath := t.TempDir()
	if errorValue := os.WriteFile(filepath.Join(workspacePath, "program.py"), []byte("a = 1\na = 1\n"), 0o644); errorValue != nil {
		t.Fatal(errorValue)
	}

	result := editFileThroughShell(context.Background(), shell{workingDirectoryPath: workspacePath}, fileEditInput{
		Path: "program.py", FindText: "a = 1", ReplaceText: "a = 2",
	})

	if result.Failure == nil {
		t.Fatal("editing one of two identical passages silently picks one, and the model never learns which")
	}
}

func TestFileWriteResultSatisfiesItsOwnContract(t *testing.T) {
	workspacePath := t.TempDir()
	toolSet := newWorkspaceToolSet(shell{workingDirectoryPath: workspacePath})

	result, errorValue := toolSet.Invoke(context.Background(), toolcontract.ToolInvocation{
		ToolName: toolcontract.FileWriteToolName,
		Input:    json.RawMessage(`{"path":"out.txt","content":"hello\n"}`),
	})

	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if result.Failure != nil {
		t.Fatalf("a tool whose own result its contract rejects can never succeed, no matter how many times the model retries: %+v", result.Failure)
	}
}

func TestTheHarnessAsksTheShellWhereItActuallyIs(t *testing.T) {
	workspacePath := t.TempDir()
	elsewhere := shell{workingDirectoryPath: workspacePath, commandPrefix: []string{"sh", "-c", "cd /tmp && \"$@\"", "--"}}

	resolved := elsewhere.resolvedWorkingDirectoryPath(context.Background())

	if resolved == workspacePath {
		t.Fatal("telling the agent a host path while its shell runs somewhere else cost one task a hundred commands spent asking where it was")
	}
}

func TestALocalShellKeepsTheWorkspaceItWasGiven(t *testing.T) {
	workspacePath := t.TempDir()

	resolved := shell{workingDirectoryPath: workspacePath}.resolvedWorkingDirectoryPath(context.Background())

	if resolved != workspacePath {
		t.Fatalf("a shell that runs here already knows where here is, got %q", resolved)
	}
}

func TestASuccessfulCommandIsVisibleAsProgress(t *testing.T) {
	result := terminalRunResult(context.Background(), shell{}, 0, "done\n", nil)

	var output terminalRunOutput
	if errorValue := json.Unmarshal(result.Output.Data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !output.Completed {
		t.Fatal("the loop reads completed to decide whether a run is getting anywhere, and a harness that never sets it can never be granted more budget however well it is doing")
	}
}

func TestAFailedCommandIsNotProgress(t *testing.T) {
	result := terminalRunResult(context.Background(), shell{}, 1, "no such file\n", nil)

	var output terminalRunOutput
	if errorValue := json.Unmarshal(result.Output.Data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if output.Completed {
		t.Fatal("a command that failed moved nothing forward")
	}
}

func TestTheShellPrefersBashWhereItExists(t *testing.T) {
	found := shell{workingDirectoryPath: t.TempDir()}.withInterpreterFound(context.Background())

	if found.interpreter() != "bash" {
		t.Fatalf("a model writing set -o pipefail gets Illegal option from dash, and every bash-ism it writes fails for a reason that is ours: %q", found.interpreter())
	}
}

func TestWithoutBashTheShellStillRuns(t *testing.T) {
	plain := shell{workingDirectoryPath: t.TempDir()}

	if plain.interpreter() != "sh" {
		t.Fatalf("sh is what every container has, so it is what an unprobed shell uses: %q", plain.interpreter())
	}
}

func TestACommandTheModelFlaggedForApprovalStillRuns(t *testing.T) {
	workspacePath := t.TempDir()
	runningShell := shell{workingDirectoryPath: workspacePath}

	result := runShellCommand(context.Background(), runningShell, terminalRunInput{
		Command:          "printf ran > flagged.txt",
		ApprovalRequired: true,
		ApprovalReason:   "installs a dependency",
	})

	if result.Failure != nil {
		t.Fatalf("this runner has one person and they started it, so there is nobody left to ask and refusing only stops the work: %+v", result.Failure)
	}
	if _, errorValue := os.Stat(filepath.Join(workspacePath, "flagged.txt")); errorValue != nil {
		t.Fatal("expected the command to have run")
	}
}

func TestOutputTooLongToReturnIsLeftSomewhereReadable(t *testing.T) {
	runningShell := shell{workingDirectoryPath: t.TempDir()}.withInterpreterFound(context.Background())

	result := terminalRunResult(context.Background(), runningShell, 0, strings.Repeat("x", maximumCapturedOutput+2000), nil)

	var output terminalRunOutput
	if errorValue := json.Unmarshal(result.Output.Data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !output.Truncated {
		t.Fatal("an output past the cap has to say it was cut")
	}
	if output.OutputPath == "" {
		t.Fatal("telling an agent its output was cut and leaving it nowhere to read the rest is a dead end")
	}
	spilled, errorValue := os.ReadFile(output.OutputPath)
	if errorValue != nil {
		t.Fatalf("the path handed to the agent has to hold the output: %v", errorValue)
	}
	if len(spilled) != maximumCapturedOutput+2000 {
		t.Fatalf("the whole output has to be there, got %d characters", len(spilled))
	}
}

func TestSpilledOutputLandsWhereThisPersonMayWrite(t *testing.T) {
	temporaryDirectory := t.TempDir()
	t.Setenv("TMPDIR", temporaryDirectory)
	runningShell := shell{workingDirectoryPath: t.TempDir()}.withInterpreterFound(context.Background())

	result := terminalRunResult(context.Background(), runningShell, 0, strings.Repeat("x", maximumCapturedOutput+2000), nil)

	var output terminalRunOutput
	if errorValue := json.Unmarshal(result.Output.Data, &output); errorValue != nil {
		t.Fatal(errorValue)
	}
	if !strings.HasPrefix(output.OutputPath, temporaryDirectory) {
		t.Fatalf("a host that gives each person their own tmp expects the spill to land there, and a shared /tmp lets one person read another's output: %q", output.OutputPath)
	}
}
