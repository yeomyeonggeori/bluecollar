package contextdescription

import "github.com/yeomyeonggeori/blueprotocol/agentcontract"

func PriorTaskContextDescription(context agentcontract.PriorTaskContext) string {
	description := agentcontract.PriorTaskDescription(context)
	if description == "" {
		return ""
	}
	return description + "\nIf the latest user message asks to deliver, retry, continue, or revise this prior task's outcome, set priorTaskReference=outcome_recovery. If it is unrelated or self-contained, set priorTaskReference=none. When recovering an outcome, infer the needed structured output formats from the prior task prompt, prior result, known contract, and latest user message."
}
