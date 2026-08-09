package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/bench"
	"github.com/yeomyeonggeori/bluecollar/intake"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func main() {
	endpointURL := flag.String("endpoint", envOrDefault("BLUECOLLAR_MODEL_ENDPOINT", "http://127.0.0.1:11434/v1"), "OpenAI-compatible base URL")
	apiKey := flag.String("api-key", os.Getenv("BLUECOLLAR_MODEL_API_KEY"), "bearer token for the endpoint, when it needs one")
	modelName := flag.String("model", envOrDefault("BLUECOLLAR_MODEL", "qwen3"), "model to ask")
	agentName := flag.String("agent-name", "the assistant", "what the agent calls itself")
	timeout := flag.Duration("timeout", 5*time.Minute, "how long one turn may run")
	workspacePath := flag.String("workspace", ".", "directory the agent's shell commands run in")
	withoutTools := flag.Bool("without-tools", false, "answer from reasoning alone, giving the agent no shell")
	execPrefix := flag.String("exec-prefix", "", "run every shell command through this wrapper, such as \"docker exec -i <container>\"")
	metricsPath := flag.String("metrics", "", "write what this turn cost, as JSON, to this path")
	withoutIntake := flag.Bool("without-intake", false, "skip the intake classifier, losing the outcome contract it builds")
	flag.Parse()

	prompt := strings.TrimSpace(strings.Join(flag.Args(), " "))
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: bluecollar [flags] <what you want done>")
		os.Exit(2)
	}

	result, errorValue := runOneTurn(runOptions{
		endpointURL:   *endpointURL,
		apiKey:        *apiKey,
		modelName:     *modelName,
		agentName:     *agentName,
		prompt:        prompt,
		timeout:       *timeout,
		workspacePath: *workspacePath,
		withoutTools:  *withoutTools,
		execPrefix:    *execPrefix,
		metricsPath:   *metricsPath,
		withoutIntake: *withoutIntake,
	})
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, "bluecollar:", errorValue)
		os.Exit(1)
	}
	printResult(result)
}

type runOptions struct {
	endpointURL   string
	apiKey        string
	modelName     string
	agentName     string
	prompt        string
	timeout       time.Duration
	workspacePath string
	withoutTools  bool
	execPrefix    string
	metricsPath   string
	withoutIntake bool
}

func runOneTurn(options runOptions) (agentcontract.AgentTurnResult, error) {
	languageModel := openaicompatible.NewProvider(options.endpointURL, options.apiKey, options.modelName)
	taskEventService := taskstate.NewTaskEventService()
	taskRunService := taskstate.NewTaskRunService(taskEventService)
	kernel := loop.NewAgentKernel(taskRunService, taskstate.NewTaskStepService())
	kernel.UseLanguageModelProvider(languageModel)

	turnContext, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()

	kernel.UseTurnOptions(agentcontract.TurnOptions{ContextWindowTokens: languageModel.ContextWindowTokens(turnContext)})

	runningShell := turnShellWithInterpreter(turnContext, options)
	workspacePath := runningShell.resolvedWorkingDirectoryPath(turnContext)
	request := agentcontract.AgentTurnRequest{
		RequesterPersonID:    "person-local",
		RequesterName:        currentUserName(),
		ConversationID:       "conversation-local",
		Prompt:               options.prompt,
		AgentIdentity:        agentcontract.AgentIdentity{Name: options.agentName},
		WorkspaceRootPath:    workspacePath,
		WorkspaceDefaultPath: workspacePath,
		ToolSet:              turnToolSet(options, runningShell),
	}

	turnDecision := decideTurn(turnContext, languageModel, request, options)
	request.PrecomputedTurnDecision = &turnDecision

	result, errorValue := kernel.RunTurn(turnContext, request)
	printLedger(taskRunService, result.TaskRun.TaskRunID)
	writeMetrics(options.metricsPath, taskRunService, result.TaskRun.TaskRunID)
	return result, errorValue
}

func printLedger(taskRunService *taskstate.TaskRunService, taskRunID string) {
	if strings.TrimSpace(taskRunID) == "" {
		return
	}
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		fmt.Fprintf(os.Stderr, "  %s  %s\n", taskEvent.Name, collapsedWhitespace(taskEvent.Body))
	}
}

func collapsedWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func routeTurn(ctx context.Context, languageModel *openaicompatible.Provider, request agentcontract.AgentTurnRequest) (agentcontract.TurnDecision, error) {
	router := intake.NewTurnRouter(languageModel, agentcontract.IntakeOptions{IsEnabled: true})
	return router.Plan(ctx, agentcontract.AgentRequest{
		RequesterPersonID: request.RequesterPersonID,
		RequesterName:     request.RequesterName,
		ConversationID:    request.ConversationID,
		Prompt:            request.Prompt,
		WorkspaceRootPath: request.WorkspaceRootPath,
		ToolSet:           request.ToolSet,
	})
}

func printResult(result agentcontract.AgentTurnResult) {
	fmt.Println(strings.TrimSpace(firstNonEmpty(result.FinishMessage, result.UserNotice, "(no reply)")))
	fmt.Fprintf(os.Stderr, "\nstatus: %s\n", result.TaskRun.Status)
	if reason := strings.TrimSpace(result.TaskRun.FailureReason); reason != "" {
		fmt.Fprintln(os.Stderr, "reason:", reason)
	}
}

func currentUserName() string {
	if name := strings.TrimSpace(os.Getenv("USER")); name != "" {
		return name
	}
	return "local"
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func turnShell(options runOptions) shell {
	return shell{
		workingDirectoryPath: options.workspacePath,
		commandPrefix:        strings.Fields(options.execPrefix),
	}
}

func turnShellWithInterpreter(ctx context.Context, options runOptions) shell {
	return turnShell(options).withInterpreterFound(ctx)
}

func turnToolSet(options runOptions, runningShell shell) *toolcontract.ToolSet {
	if options.withoutTools {
		return nil
	}
	return newWorkspaceToolSet(runningShell)
}

func writeMetrics(metricsPath string, taskRunService *taskstate.TaskRunService, taskRunID string) {
	if strings.TrimSpace(metricsPath) == "" {
		return
	}
	metrics := bench.MeasureTaskRun(taskRunID, taskRunService.ListTaskEvent(taskRunID))
	document, errorValue := json.MarshalIndent(metrics, "", "  ")
	if errorValue != nil {
		fmt.Fprintln(os.Stderr, "bluecollar: could not measure the turn:", errorValue)
		return
	}
	if writeError := os.WriteFile(metricsPath, document, 0o644); writeError != nil {
		fmt.Fprintln(os.Stderr, "bluecollar: could not write the measurements:", writeError)
	}
}

func decideTurn(ctx context.Context, languageModel *openaicompatible.Provider, request agentcontract.AgentTurnRequest, options runOptions) agentcontract.TurnDecision {
	if options.withoutIntake {
		return boundedTaskDecision()
	}
	turnDecision, routingError := routeTurn(ctx, languageModel, request)
	if routingError != nil {
		fmt.Fprintln(os.Stderr, "bluecollar: classifying the request failed, doing it anyway:", routingError)
		return boundedTaskDecision()
	}
	return startingTheTaskItWasGiven(turnDecision)
}

func startingTheTaskItWasGiven(turnDecision agentcontract.TurnDecision) agentcontract.TurnDecision {
	if turnDecision.Route == agentcontract.TurnRouteStartTask || turnDecision.Route == agentcontract.TurnRouteContinueTask {
		return turnDecision
	}
	fmt.Fprintf(os.Stderr, "bluecollar: the classifier answered %q; the runner was given a task, so it starts one\n", turnDecision.Route)
	turnDecision.Route = agentcontract.TurnRouteStartTask
	turnDecision.Classification = agentcontract.IntakeClassificationBoundedTask
	if turnDecision.TaskShape == agentcontract.TaskShapeImmediateReply || turnDecision.TaskShape == "" {
		turnDecision.TaskShape = agentcontract.TaskShapeMaintenanceTask
	}
	return turnDecision
}

func boundedTaskDecision() agentcontract.TurnDecision {
	return agentcontract.TurnDecision{
		Route:          agentcontract.TurnRouteStartTask,
		Classification: agentcontract.IntakeClassificationBoundedTask,
		TaskShape:      agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:      agentcontract.TaskLevelLow,
		Reason:         "the runner was given one thing to do",
	}
}
