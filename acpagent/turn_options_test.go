package acpagent

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/bluecollar/turnoptions"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

func driveATurnWithTurnOptions(t *testing.T, turnOptions turnoptions.TurnOptions) (*hostClient, *scriptedLanguageModel) {
	t.Helper()
	hostCalls := []hostToolCall{}
	catalogClientTransport, catalogServerTransport := mcp.NewInMemoryTransports()
	go publishedCatalog(t, &hostCalls).Run(t.Context(), catalogServerTransport)
	languageModel := &scriptedLanguageModel{}
	options := testOptions(languageModel)
	options.TurnOptions = turnOptions
	host, _ := driveOneTurnWithOptions(t, catalogClientTransport, options, nil)
	return host, languageModel
}

func TestAConfiguredContextWindowReachesTheKernel(t *testing.T) {
	host, _ := driveATurnWithTurnOptions(t, turnoptions.TurnOptions{ContextWindowTokens: 128000})

	for _, record := range host.keptLedger() {
		if record.Name != agentcontract.TaskEventAgentConversationBudget {
			continue
		}
		var body struct {
			ContextWindowTokens int `json:"contextWindowTokens"`
		}
		if errorValue := json.Unmarshal(record.Body, &body); errorValue != nil || body.ContextWindowTokens != 128000 {
			t.Fatalf("the kernel budgeted the conversation on %+v, expected the window the host configured: %v", body, errorValue)
		}
		return
	}
	t.Fatalf("the turn recorded no conversation budget, got %v", host.ledgerEventNames())
}

func TestConfiguredGenerationOptionsReachTheModelsActionCalls(t *testing.T) {
	seed := int64(41)
	temperature := 0.2

	_, languageModel := driveATurnWithTurnOptions(t, turnoptions.TurnOptions{GenerationOptions: model.GenerationOptions{Seed: &seed, Temperature: &temperature}})

	if len(languageModel.generation) == 0 {
		t.Fatal("the model was never asked for an action")
	}
	for _, generation := range languageModel.generation {
		if generation.Seed == nil || *generation.Seed != seed || generation.Temperature == nil || *generation.Temperature != temperature {
			t.Fatalf("an action call ran on %+v, expected the seed and temperature the host configured", generation)
		}
	}
}

func TestAConfiguredRouterModelRoutesTheTurn(t *testing.T) {
	hostCalls := []hostToolCall{}
	catalogClientTransport, catalogServerTransport := mcp.NewInMemoryTransports()
	go publishedCatalog(t, &hostCalls).Run(t.Context(), catalogServerTransport)
	taskModel := &scriptedLanguageModel{}
	routerModel := &scriptedLanguageModel{}
	options := testOptions(taskModel)
	options.RouterLanguageModel = routerModel

	driveOneTurnWithOptions(t, catalogClientTransport, options, nil)

	if routerModel.routedCount == 0 {
		t.Fatal("the model the host named for routing was never asked to route")
	}
}
