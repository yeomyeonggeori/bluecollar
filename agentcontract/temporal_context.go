package agentcontract

import (
	"strings"
	"time"
)

const (
	CompanyZoneFallbackUnset      = "unset"
	CompanyZoneFallbackUnloadable = "unloadable"
)

func BuildTemporalContextDescription(environmentNow time.Time, timeZone string) string {
	if environmentNow.IsZero() {
		return ""
	}
	location := CompanyLocation(timeZone)
	localTime := environmentNow.In(location)
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

func CompanyLocation(timeZone string) *time.Location {
	location, _ := companyLocationWithFallbackReason(timeZone)
	return location
}

func CompanyZoneFallbackReason(timeZone string) string {
	_, fallbackReason := companyLocationWithFallbackReason(timeZone)
	return fallbackReason
}

func companyLocationWithFallbackReason(timeZone string) (*time.Location, string) {
	named := strings.TrimSpace(timeZone)
	if named == "" {
		return time.Local, CompanyZoneFallbackUnset
	}
	location, errorValue := time.LoadLocation(named)
	if errorValue != nil {
		return time.Local, CompanyZoneFallbackUnloadable
	}
	return location, ""
}
