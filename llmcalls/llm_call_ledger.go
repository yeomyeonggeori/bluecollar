package llmcalls

import (
	"slices"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/model"
)

const TurnRouterSchemaName = "bluecollar_turn_router"

const AttachmentDescriptionSchemaName = "bluecollar_attachment_description"

const AgentActionSchemaName = "bluecollar_agent_turn_action"

var IntakeSchemaNames = []string{TurnRouterSchemaName, AttachmentDescriptionSchemaName}

type IntakeCallLedger struct {
	Records     []agentcontract.LLMCallRecord
	SchemaNames []string
}

func (ledger *IntakeCallLedger) Observe(record agentcontract.LLMCallRecord) {
	if !ledger.includes(record) {
		return
	}
	ledger.Records = append(ledger.Records, record)
}

func (ledger *IntakeCallLedger) includes(record agentcontract.LLMCallRecord) bool {
	return record.Kind == agentcontract.LLMCallKindDecision || slices.Contains(ledger.SchemaNames, record.SchemaName)
}

func (ledger *IntakeCallLedger) LanguageModel(provider model.LanguageModelProvider) model.LanguageModelProvider {
	return agentcontract.ObserveLanguageModel(provider, ledger.Observe)
}
