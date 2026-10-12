package acpagent

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type requesterContextLanguageModel struct {
	*scriptedLanguageModel
	requestContexts []model.RequestContext
}

func (languageModel *requesterContextLanguageModel) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	if request.StructuredOutputSchema.Name == "bluecollar_agent_turn_action" {
		languageModel.requestContexts = append(languageModel.requestContexts, model.RequestContextFromContext(ctx))
	}
	return languageModel.scriptedLanguageModel.GenerateStructuredResponse(ctx, request)
}

func TestHostRequesterEmailAndPlatformIdentitySurviveTheKernel(t *testing.T) {
	hostCalls := []hostToolCall{}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go publishedCatalog(t, &hostCalls).Run(t.Context(), serverTransport)
	languageModel := &requesterContextLanguageModel{scriptedLanguageModel: &scriptedLanguageModel{}}
	metadata := map[string]any{TurnRequestMetaKey: agentcontract.AgentTurnRequest{
		RequesterIdentity: agentcontract.RequesterIdentity{
			RequesterPersonID:       "sample-person-id",
			RequesterName:           "이샘플",
			RequesterEmail:          "sample@example.com",
			RequesterPlatformUserID: "sample-platform-id",
		},
		Platform: "virtual",
	}}
	driveOneTurnWithOptions(t, clientTransport, testOptions(languageModel), metadata)
	if len(languageModel.requestContexts) == 0 {
		t.Fatal("the agent made no model request")
	}
	for _, requestContext := range languageModel.requestContexts {
		if requestContext.RequesterEmail != "sample@example.com" || requestContext.RequesterPlatformUserID != "sample-platform-id" || requestContext.Platform != "virtual" {
			t.Fatalf("authenticated identity was lost: %+v", requestContext)
		}
	}
	for _, prompt := range languageModel.actionPrompts {
		if !strings.Contains(prompt, "<sample@example.com>") {
			t.Fatal("the model prompt lost the authenticated requester email")
		}
	}
}

func TestStandaloneLedgerIdentityIsNotAnAuthenticatedRequester(t *testing.T) {
	hostCalls := []hostToolCall{}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go publishedCatalog(t, &hostCalls).Run(t.Context(), serverTransport)
	languageModel := &requesterContextLanguageModel{scriptedLanguageModel: &scriptedLanguageModel{}}
	driveOneTurnWithOptions(t, clientTransport, testOptions(languageModel), nil)
	if len(languageModel.requestContexts) == 0 {
		t.Fatal("the agent made no model request")
	}
	for _, requestContext := range languageModel.requestContexts {
		if requestContext.RequesterPersonID != "" {
			t.Fatalf("an internal ledger identity became an authenticated requester: %q", requestContext.RequesterPersonID)
		}
	}
	for _, prompt := range languageModel.actionPrompts {
		if strings.Contains(prompt, "[personID bluecollar]") {
			t.Fatal("the model was given an invented requester identity")
		}
	}
}
