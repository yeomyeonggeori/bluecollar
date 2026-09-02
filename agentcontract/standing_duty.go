package agentcontract

import "strings"

type StandingDuty struct {
	Name        string
	Description string
	Instruction string
	ToolNames   []string
}

var standingDuties = []StandingDuty{
	{
		Name:        "calendar_upkeep",
		Description: "a specific meeting, deadline, or scheduled event that should be created or updated as a calendar event right now",
		Instruction: "Record the concrete meeting, deadline, or scheduled event the overheard message states as a calendar entry. List the existing entries around that date first and update the matching one instead of creating a duplicate. When the message states nothing concrete enough to put on a calendar, finish without changing anything.",
		ToolNames:   []string{"event_list", "event_add", "event_update", "conversation_history", "memory_search"},
	},
	{
		Name:        "team_flow_update",
		Description: "a specific work task assigned to a person that should be added, or whose status or details should be updated or completed right now",
		Instruction: "Record the concrete work task the overheard message assigns, or update the existing task whose status or details it changes. List the existing tasks first and update the matching one instead of creating a duplicate. When the message assigns nothing concrete enough to track, finish without changing anything.",
		ToolNames:   []string{"task_list", "task_add", "task_update", "conversation_history", "memory_search"},
	},
}

func StandingDuties() []StandingDuty {
	return append([]StandingDuty{}, standingDuties...)
}

func StandingDutyByName(dutyName string) (StandingDuty, bool) {
	trimmedDutyName := strings.TrimSpace(dutyName)
	for _, duty := range standingDuties {
		if duty.Name == trimmedDutyName {
			return duty, true
		}
	}
	return StandingDuty{}, false
}

const ambientDutyContextHeading = "Ambient duty context"

func AmbientDutyInstructionPrompt(duty StandingDuty, overheardMessage string, senderName string) string {
	return strings.Join([]string{
		ambientDutyContextHeading + " (" + duty.Name + ")",
		duty.Instruction,
		"",
		ambientDutyOverheardHeading(senderName),
		strings.TrimSpace(overheardMessage),
	}, "\n")
}

func ambientDutyOverheardHeading(senderName string) string {
	trimmedSenderName := strings.TrimSpace(senderName)
	if trimmedSenderName == "" {
		return "Overheard message:"
	}
	return "Overheard message from " + trimmedSenderName + ":"
}

func AmbientDutyTurnDecision(duty StandingDuty, responseLanguage string) TurnDecision {
	return TurnDecision{
		Route:            TurnRouteStartTask,
		Classification:   IntakeClassificationBoundedTask,
		TaskShape:        TaskShapeMaintenanceTask,
		TaskLevel:        TaskLevelLow,
		ResponseLanguage: responseLanguage,
		Reason:           "ambient_duty_" + duty.Name,
	}
}
