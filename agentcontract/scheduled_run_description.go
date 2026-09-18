package agentcontract

const ScheduledRunReading = "its text is the work to carry out at this moment; the schedule that repeats it already exists, so nothing in it about when or how often is a request to schedule, remind, change, or cancel anything; judge everything else about it as for any message."

func ScheduledRunDescriptionForPrompt(scheduledRun ScheduledRunContext) string {
	if scheduledRun.IsEmpty() {
		return ""
	}
	return "Scheduled run:\n" + marshalEventBody(scheduledRun) +
		"\nThe user message is this schedule firing now: " + ScheduledRunReading
}
