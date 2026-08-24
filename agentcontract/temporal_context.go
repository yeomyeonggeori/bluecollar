package agentcontract

import (
	"strings"
	"time"
)

// The date comes from outside. A host that knows the clock of the world being worked on states
// it — a simulated world, a replayed dataset, a device in another timezone — and where none has,
// the runtime does not know it. Naming the machine's clock instead is a claim about that world
// which the runtime is not entitled to make.
func BuildTemporalContextDescription(environmentNow time.Time) string {
	if environmentNow.IsZero() {
		return unknownDateContext()
	}
	location := TemporalContextLocation()
	currentTime := environmentNow
	localTime := currentTime.In(location)
	return strings.Join([]string{
		"Runtime temporal context:",
		"Current date: " + localTime.Format("2006-01-02"),
		"Current weekday: " + localTime.Weekday().String(),
		"Current time: " + localTime.Format("15:04"),
		"Time zone: " + location.String(),
		buildCurrentWeekDescription(localTime),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

func unknownDateContext() string {
	return strings.Join([]string{
		"Runtime temporal context:",
		"This runtime was not told the current date. Read it from the system you are operating before answering anything that turns on a date; the machine you run on may keep a different one.",
	}, "\n")
}

func buildCurrentWeekDescription(localTime time.Time) string {
	daysSinceMonday := (int(localTime.Weekday()) + 6) % 7
	monday := localTime.AddDate(0, 0, -daysSinceMonday)
	weekdays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	anchors := make([]string, 0, len(weekdays))
	for dayOffset, weekday := range weekdays {
		anchors = append(anchors, weekday+"="+monday.AddDate(0, 0, dayOffset).Format("2006-01-02"))
	}
	return "Current week: " + strings.Join(anchors, ", ")
}

func TemporalContextLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue == nil {
		return location
	}
	return time.FixedZone("Asia/Seoul", 9*60*60)
}
