package loop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestObservedURLInReplyIgnoresUncontractedOutput(t *testing.T) {
	toolSet := newTestToolSet([]string{"external.publish"})
	observations := []turnObservation{{
		ObservationID: "obs-001",
		Tool:          "external.publish",
		Output: toolcontract.ToolOutput{
			Content: `{"publicURL":"https://portfolio.example"}`,
			Data:    json.RawMessage(`{}`),
		},
	}}

	if message := missingObservedURLInReply(toolSet, observations, "Published it."); message != "" {
		t.Fatalf("uncontracted URL must not be demanded in the reply: %q", message)
	}
}

func TestObservedURLInReplyUsesValidatedEffects(t *testing.T) {
	toolSet, observation := canonicalLinkObservation("external.publish", "https://portfolio.example")

	if message := missingObservedURLInReply(toolSet, []turnObservation{observation}, "Published it."); !strings.Contains(message, "https://portfolio.example") {
		t.Fatalf("expected exact canonical URL to be demanded, got %q", message)
	}

	observation.Effects[0].URL = "https://different.example"
	if message := missingObservedURLInReply(toolSet, []turnObservation{observation}, "Published it."); message != "" {
		t.Fatalf("mismatched effect identity must not be demanded: %q", message)
	}
}

func TestObservedURLInReplyIgnoresWhatALookupFound(t *testing.T) {
	descriptor := canonicalLinkToolDefinition("web_search")
	descriptor.SideEffectClass = toolcontract.ToolSideEffectRead
	result := canonicalLinkToolResult("https://reference.example")
	toolSet := newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor})
	observation := turnObservation{ObservationID: "obs-001", Tool: "web_search", Output: result.Output, Effects: result.Effects}

	if message := missingObservedURLInReply(toolSet, []turnObservation{observation}, "Here is what I found."); message != "" {
		t.Fatalf("a URL a lookup found is not one the reply must carry: %q", message)
	}
}

func TestCompletionFactsRequireExactObservedURLInReply(t *testing.T) {
	toolSet, observation := canonicalLinkObservation("site_serve", "https://portfolio.example")
	request := AgentTurnRequest{ToolSet: toolSet}

	wrongURL := validateCompletionFacts(request, []turnObservation{observation}, satisfiedFinishDocument("Deployed it: https://different.example"))
	if wrongURL.IsSatisfied || !strings.Contains(wrongURL.Message, "https://portfolio.example") {
		t.Fatalf("expected exact observed URL requirement, got %+v", wrongURL)
	}

	exactURL := validateCompletionFacts(request, []turnObservation{observation}, satisfiedFinishDocument("Deployed it: https://portfolio.example/"))
	if !exactURL.IsSatisfied {
		t.Fatalf("expected normalized exact URL to pass, got %+v", exactURL)
	}
}

func canonicalLinkObservation(toolName string, publicURL string) (*toolcontract.ToolSet, turnObservation) {
	descriptor := canonicalLinkToolDefinition(toolName)
	result := canonicalLinkToolResult(publicURL)
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{descriptor}), turnObservation{
		ObservationID: "obs-001",
		Tool:          toolName,
		Output:        result.Output,
		Effects:       result.Effects,
	}
}

func canonicalLinkToolDefinition(toolName string) toolcontract.ToolDefinition {
	descriptor := testToolDescriptor(toolName)
	descriptor.OutputSchema = json.RawMessage(`{"type":"object","properties":{"publicURL":{"type":"string"}},"required":["publicURL"],"additionalProperties":false}`)
	descriptor.ResultContract = &toolcontract.ToolResultContract{
		Schema: json.RawMessage(`{"type":"object","properties":{"publicURL":{"type":"string"}},"required":["publicURL"],"additionalProperties":false}`),
		Effects: []toolcontract.ResourceEffectContract{{
			ObjectType:     "website",
			Effect:         "published",
			ResultField:    "publicURL",
			EffectIdentity: "url",
		}},
	}
	return descriptor
}

func canonicalLinkToolResult(publicURL string) toolcontract.ToolResult {
	outputData := json.RawMessage(marshalEventBody(map[string]string{"publicURL": publicURL}))
	return toolcontract.ToolResult{
		Output:  toolcontract.ToolOutput{Content: string(outputData), Data: outputData},
		Effects: []toolcontract.ResourceEffect{{ObjectType: "website", Effect: "published", URL: publicURL}},
	}
}
