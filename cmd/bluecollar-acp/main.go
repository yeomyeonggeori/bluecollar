// Command bluecollar-acp runs the agent loop as an Agent Client Protocol
// agent, so any host that speaks ACP can drive it. It owns no tools: the tool
// catalog arrives on the MCP servers the host names when it opens a session.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/bluecollar/attribution"
	"github.com/yeomyeonggeori/bluecollar/decisionconfig"
	"github.com/yeomyeonggeori/bluecollar/turnoptions"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
)

func main() {
	endpointURL := flag.String("endpoint", envOrDefault("BLUECOLLAR_LLM_ENDPOINT", "http://127.0.0.1:8080/v1"), "OpenAI-compatible endpoint the loop reasons through")
	apiKey := flag.String("api-key", "", "API key for that endpoint; default $BLUECOLLAR_LLM_API_KEY")
	modelName := flag.String("model", os.Getenv("BLUECOLLAR_LLM_MODEL"), "model name to request")
	agentName := flag.String("name", envOrDefault("BLUECOLLAR_AGENT_NAME", "bluecollar"), "the name this agent answers to")
	isStructuredOutputOnly := flag.Bool("structured-output-only", os.Getenv("BLUECOLLAR_LLM_STRUCTURED_OUTPUT_ONLY") != "", "reason through structured responses only, never native tool-calling chat; default set by $BLUECOLLAR_LLM_STRUCTURED_OUTPUT_ONLY")
	contextWindowTokens := flag.Int("context-window-tokens", environmentInteger("BLUECOLLAR_CONTEXT_WINDOW_TOKENS"), "the model's context window; default the endpoint's own answer, $BLUECOLLAR_CONTEXT_WINDOW_TOKENS")
	generation := generationFlags{}
	flag.Func("seed", "sampling seed sent with every action call; default $BLUECOLLAR_SEED", generation.setSeed)
	flag.Func("temperature", "sampling temperature sent with every action call; default $BLUECOLLAR_TEMPERATURE", generation.setTemperature)
	generation.setFromEnvironment()
	flag.Parse()

	if *modelName == "" {
		log.Fatal("bluecollar-acp: no model named; pass -model or set BLUECOLLAR_LLM_MODEL")
	}

	endpointModel := openaicompatible.NewProvider(*endpointURL, flagOrEnvironment(*apiKey, "BLUECOLLAR_LLM_API_KEY"), *modelName).WithAttribution(attribution.Self)
	var languageModel model.LanguageModelProvider = endpointModel
	if *isStructuredOutputOnly {
		languageModel = structuredOutputOnly{languageModel}
	}
	errorValue := acpagent.Serve(acpagent.Options{
		AgentName:      *agentName,
		LanguageModels: agentcontract.TaskTierLanguageModels{Low: languageModel},
		DecisionModel:  decisionconfig.ConfiguredDecisionModel(os.Stderr),
		TurnOptions: turnoptions.TurnOptions{
			ContextWindowTokens: windowTokens(*contextWindowTokens, endpointModel),
			GenerationOptions:   generation.options(),
		},
	}, os.Stdout, os.Stdin)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

type structuredOutputOnly struct{ model.LanguageModelProvider }

func windowTokens(configured int, endpointModel *openaicompatible.Provider) int {
	if configured > 0 {
		return configured
	}
	return endpointModel.ContextWindowTokens(context.Background())
}

func environmentInteger(environmentName string) int {
	value, errorValue := strconv.Atoi(os.Getenv(environmentName))
	if errorValue != nil {
		return 0
	}
	return value
}

func envOrDefault(environmentName string, fallback string) string {
	if value := os.Getenv(environmentName); value != "" {
		return value
	}
	return fallback
}

func flagOrEnvironment(value string, environmentName string) string {
	if value != "" {
		return value
	}
	return os.Getenv(environmentName)
}

func init() {
	log.SetOutput(os.Stderr)
}
