package agentcontract

func (turnRequest AgentTurnRequest) RoutingRequest() AgentRequest {
	return AgentRequest{
		RequesterPersonID:    turnRequest.RequesterPersonID,
		RequesterName:        turnRequest.RequesterName,
		RequesterCallingName: turnRequest.RequesterCallingName,
		RequesterHandle:      turnRequest.RequesterHandle,
		AgentIdentity:        turnRequest.AgentIdentity,
		ConversationID:       turnRequest.ConversationID,
		ConversationType:     turnRequest.ConversationType,
		Prompt:               turnRequest.Prompt,
		InputParts:           turnRequest.InputParts,
		ResponseLanguage:     turnRequest.ResponseLanguage,
		VisibleContext:       turnRequest.VisibleContext,
		ScheduledRun:         turnRequest.ScheduledRun,
		ActiveGoal:           turnRequest.ActiveGoal,
		PriorTask:            turnRequest.PriorTask,
		TurnStartedAt:        turnRequest.TurnStartedAt,
		EnvironmentNow:       turnRequest.EnvironmentNow,
		Company:              turnRequest.Company,
		ToolSet:              turnRequest.ToolSet,
	}
}
