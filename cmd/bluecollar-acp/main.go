// Command bluecollar-acp runs the agent loop as an Agent Client Protocol
// agent, so any host that speaks ACP can drive it. It owns no tools: the tool
// catalog arrives on the MCP servers the host names when it opens a session.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/yeomyeonggeori/bluecollar/acpagent"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

func main() {
	endpointURL := flag.String("endpoint", envOrDefault("BLUECOLLAR_LLM_ENDPOINT", "http://127.0.0.1:8080/v1"), "OpenAI-compatible endpoint the loop reasons through")
	apiKey := flag.String("api-key", "", "API key for that endpoint; default $BLUECOLLAR_LLM_API_KEY")
	modelName := flag.String("model", os.Getenv("BLUECOLLAR_LLM_MODEL"), "model name to request")
	agentName := flag.String("name", envOrDefault("BLUECOLLAR_AGENT_NAME", "bluecollar"), "the name this agent answers to")
	isStructuredOutputOnly := flag.Bool("structured-output-only", os.Getenv("BLUECOLLAR_LLM_STRUCTURED_OUTPUT_ONLY") != "", "reason through structured responses only, never native tool-calling chat; default set by $BLUECOLLAR_LLM_STRUCTURED_OUTPUT_ONLY")
	flag.Parse()

	if *modelName == "" {
		log.Fatal("bluecollar-acp: no model named; pass -model or set BLUECOLLAR_LLM_MODEL")
	}

	var languageModel model.LanguageModelProvider = openaicompatible.NewProvider(*endpointURL, flagOrEnvironment(*apiKey, "BLUECOLLAR_LLM_API_KEY"), *modelName)
	if *isStructuredOutputOnly {
		languageModel = structuredOutputOnly{languageModel}
	}
	errorValue := acpagent.Serve(acpagent.Options{
		AgentName:      *agentName,
		LanguageModels: agentcontract.TaskTierLanguageModels{Low: languageModel},
		DecisionModel:  decisions.ConfiguredDecisionModel(os.Stderr),
	}, os.Stdout, os.Stdin)
	if errorValue != nil {
		log.Fatal(errorValue)
	}
}

type structuredOutputOnly struct{ model.LanguageModelProvider }

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
