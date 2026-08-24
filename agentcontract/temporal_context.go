package agentcontract

import (
	"strings"
	"time"
)

// A host that knows its environment keeps its own clock states it, and that is the clock the
// work happens on: a simulated world, a replayed dataset, a device in another timezone. The
// turn's own start time still drives the budget, which is measured against the machine.
func BuildTemporalContextDescription(turnStartedAt time.Time, environmentNow time.Time) string {
	currentTime := turnStartedAt
	environmentStatedItsClock := !environmentNow.IsZero()
	if environmentStatedItsClock {
		currentTime = environmentNow
	}
	if currentTime.IsZero() {
		currentTime = time.Now()
	}
	location := TemporalContextLocation()
	localTime := currentTime.In(location)
	return strings.Join([]string{
		"Runtime temporal context:",
		currentDateLine(localTime, environmentStatedItsClock),
		"Current weekday: " + localTime.Weekday().String(),
		"Current time: " + localTime.Format("15:04"),
		"Time zone: " + location.String(),
		buildCurrentWeekDescription(localTime),
		"Resolve relative dates such as today, tomorrow, next Friday, 오늘, 내일, and 다음 주 from this context before choosing tool inputs.",
	}, "\n")
}

// Unattributed, this reads as the date of the world being worked on, and the runtime has no way
// to know that. A system the agent operates through can keep its own clock, and an agent that
// filtered on the wrong year has no way to notice.
func currentDateLine(localTime time.Time, environmentStatedItsClock bool) string {
	if environmentStatedItsClock {
		return "Current date: " + localTime.Format("2006-01-02")
	}
	return "Current date on the machine running you: " + localTime.Format("2006-01-02") +
		". A system you operate through may keep its own clock; when a request turns on its dates, read them from that system."
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
