package contextdescription

import "github.com/yeomyeonggeori/blueprotocol/agentcontract"

func ActiveGoalDescription(activeGoal agentcontract.ActiveGoal) string {
	return ActiveGoalDescriptionForPrompt(activeGoal, "")
}

func ActiveGoalDescriptionForPrompt(activeGoal agentcontract.ActiveGoal, currentPrompt string) string {
	description := agentcontract.ActiveGoalDescription(activeGoal, currentPrompt)
	if description == "" {
		return ""
	}
	return description + "\nrequiredNextTools and selectedToolNames are intake suggestions, not commands: when a listed tool contradicts what the user visibly asked for, use the tool that actually fulfills the request."
}
