package agentcontract

import (
	"strings"
	"time"
)

func BuildTemporalContextDescription(environmentNow time.Time) string {
	if environmentNow.IsZero() {
		return ""
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
