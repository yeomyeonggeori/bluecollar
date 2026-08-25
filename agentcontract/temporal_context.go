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
		"Now: " + localTime.Format("2006-01-02 (Mon) 15:04 -07:00") + " " + location.String(),
		buildCurrentWeekDescription(localTime),
		"Resolve relative dates (오늘, 내일, 다음 주, next Friday) from this.",
	}, "\n")
}

func unknownDateContext() string {
	return strings.Join([]string{
		"Now: not known to this runtime. Before answering anything that turns on a date, take today from the system you are operating — its own clock, apps, or records — never from this shell's clock, which can belong to a different machine than the one whose data you are reading.",
	}, "\n")
}

func buildCurrentWeekDescription(localTime time.Time) string {
	daysSinceMonday := (int(localTime.Weekday()) + 6) % 7
	monday := localTime.AddDate(0, 0, -daysSinceMonday)
	anchors := make([]string, 0, 7)
	for dayOffset := 0; dayOffset < 7; dayOffset++ {
		anchors = append(anchors, monday.AddDate(0, 0, dayOffset).Format("Mon 01-02"))
	}
	return "This week: " + strings.Join(anchors, ", ")
}

func TemporalContextLocation() *time.Location {
	location, errorValue := time.LoadLocation("Asia/Seoul")
	if errorValue == nil {
		return location
	}
	return time.FixedZone("Asia/Seoul", 9*60*60)
}
