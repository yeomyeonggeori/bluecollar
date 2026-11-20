package acpagent

import (
	"context"
	"errors"
	"net/http"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yeomyeonggeori/blueprotocol/acpfailure"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

type failingGatewayDecisionModel struct{}

func (failingGatewayDecisionModel) Decide(context.Context, model.DecisionRequest) (model.DecisionResponse, error) {
	return model.DecisionResponse{}, model.EndpointError{
		StatusCode: http.StatusBadGateway,
		Reason:     errors.New("decisions endpoint answered 502 Bad Gateway: "),
	}
}

func TestAFailedTurnTellsTheHostWhyTheModelFailed(t *testing.T) {
	hostCalls := []hostToolCall{}
	catalogServer := publishedCatalog(t, &hostCalls)
	catalogClientTransport, catalogServerTransport := mcp.NewInMemoryTransports()
	go catalogServer.Run(t.Context(), catalogServerTransport)
	options := testOptions(&scriptedLanguageModel{})
	options.DecisionModel = failingGatewayDecisionModel{}

	_, _, errorValue := promptOneTurn(t, catalogClientTransport, options, nil)

	var requestError *acp.RequestError
	if !errors.As(errorValue, &requestError) {
		t.Fatalf("expected the turn to fail over ACP, got %#v", errorValue)
	}
	if cause := acpfailure.CauseOf(requestError); cause != agentcontract.FailureCauseTransient {
		t.Fatalf("the host words the failure for a person and needs to know a 502 passes, got %q in %+v", cause, requestError)
	}
}
