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
		"Current time: " + localTime.Format("15:04") + " (" + localTime.Format(time.RFC3339) + ")",
		// The name alone leaves the offset to be recalled rather than read, and a
		// small model recalls it wrong.
		"Time zone: " + location.String() + " (UTC" + localTime.Format("-07:00") + ")",
		buildCurrentWeekDescription(localTime),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

func unknownDateContext() string {
	return strings.Join([]string{
		"Runtime temporal context:",
		"This runtime was not told the current date. Before answering anything that turns on a date, read the clock of the system you are operating: the shell's date command reports the machine's clock, which is not necessarily the operated system's. When the system you are working in reports its own dates or times and they disagree with the machine, the operated system's clock decides.",
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
