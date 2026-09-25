package loop

import (
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func normalizePriorTaskContext(context PriorTaskContext) PriorTaskContext {
	context.TaskRunID = strings.TrimSpace(context.TaskRunID)
	context.Status = strings.TrimSpace(context.Status)
	context.Prompt = strings.TrimSpace(context.Prompt)
	context.Result = strings.TrimSpace(context.Result)
	context.FailureReason = strings.TrimSpace(context.FailureReason)
	context.OutcomeContract = normalizeOutcomeContract(context.OutcomeContract)
	context.RequestedOutputFormats = normalizeRequestedOutputFormats(context.RequestedOutputFormats)
	return context
}

func priorTaskContextDescription(context PriorTaskContext) string {
	return agentcontract.PriorTaskContextDescription(normalizePriorTaskContext(context))
}

func priorTaskContextHasContent(context PriorTaskContext) bool {
	return strings.TrimSpace(context.TaskRunID) != "" ||
		strings.TrimSpace(context.Prompt) != "" ||
		OutcomeContractHasRequirements(context.OutcomeContract) ||
		len(context.RequestedOutputFormats) > 0
}

func applyPriorTaskOutcomeRecovery(request AgentRequest, decision IntakeDecision) (AgentRequest, IntakeDecision) {
	if normalizePriorTaskReference(decision.PriorTaskReference) != PriorTaskReferenceOutcomeRecovery {
		return request, decision
	}
	priorTask := normalizePriorTaskContext(request.PriorTask)
	if !priorTaskContextHasContent(priorTask) {
		decision.PriorTaskReference = PriorTaskReferenceNone
		return request, decision
	}
	request.ActiveGoal = ActiveGoal{
		OriginalInstruction: firstNonEmptyString(priorTask.Prompt, request.Prompt),
		CurrentObjective:    request.Prompt,
		KnownContext:        priorTaskKnownContext(priorTask),
		Status:              ActiveGoalStatusActive,
	}
	return request, decision
}

func priorTaskKnownContext(priorTask PriorTaskContext) []string {
	values := []string{}
	if priorTask.TaskRunID != "" {
		values = append(values, "Prior task run: "+priorTask.TaskRunID)
	}
	if priorTask.Status != "" {
		values = append(values, "Prior task status: "+priorTask.Status)
	}
	values = append(values, "Reassess the previous assistant's interpretation against the user's messages and recorded attempts in Prior task context. Its report is not an established fact.")
	return values
}
