package llmcalls

import (
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestIntakeCallLedgerPreservesMissingModelTier(t *testing.T) {
	ledger := &IntakeCallLedger{SchemaNames: IntakeSchemaNames}
	ledger.Observe(agentcontract.LLMCallRecord{SchemaName: TurnRouterSchemaName})

	if len(ledger.Records) != 1 || ledger.Records[0].IsError || ledger.Records[0].ModelTier != "" {
		t.Fatalf("expected missing router tier to remain observational, got %+v", ledger.Records)
	}
}
