// Command bluecollar-acp runs the agent loop as an Agent Client Protocol
// agent, so any host that speaks ACP can drive it. It owns no tools: the tool
// catalog arrives on the MCP servers the host names when it opens a session.
package main

import (
	"flag"
	"log"
	"os"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

const requesterPersonID = "bluecollar"

func main() {
	endpointURL := flag.String("endpoint", envOrDefault("BLUECOLLAR_LLM_ENDPOINT", "http://127.0.0.1:8080/v1"), "OpenAI-compatible endpoint the loop reasons through")
	apiKey := flag.String("api-key", os.Getenv("BLUECOLLAR_LLM_API_KEY"), "API key for that endpoint")
	modelName := flag.String("model", os.Getenv("BLUECOLLAR_LLM_MODEL"), "model name to request")
	agentName := flag.String("name", envOrDefault("BLUECOLLAR_AGENT_NAME", "bluecollar"), "the name this agent answers to")
	flag.Parse()

	if *modelName == "" {
		log.Fatal("bluecollar-acp: no model named; pass -model or set BLUECOLLAR_LLM_MODEL")
	}

	languageModel := openaicompatible.NewProvider(*endpointURL, *apiKey, *modelName)
	runningAgent := newAgent(languageModel, *agentName)
	connection := acp.NewAgentSideConnection(runningAgent, os.Stdout, os.Stdin)
	runningAgent.sessionUpdates = connection
	<-connection.Done()
}

func envOrDefault(environmentName string, fallback string) string {
	if value := os.Getenv(environmentName); value != "" {
		return value
	}
	return fallback
}

func init() {
	log.SetOutput(os.Stderr)
}
