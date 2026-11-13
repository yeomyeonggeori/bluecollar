package approval

import (
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

var koreanWeekdays = []string{"일", "월", "화", "수", "목", "금", "토"}
var japaneseWeekdays = []string{"日", "月", "火", "水", "木", "金", "土"}

func momentForPerson(written string, language string, requesterTimeZone string) string {
	instant, errorValue := time.Parse(time.RFC3339, strings.TrimSpace(written))
	if errorValue != nil {
		return written
	}
	location, isKnown := requesterLocation(requesterTimeZone)
	if !isKnown {
		return wallClockIn(language, instant) + " (" + utcOffsetOf(instant) + ")"
	}
	return wallClockIn(language, instant.In(location))
}

func requesterLocation(requesterTimeZone string) (*time.Location, bool) {
	named := strings.TrimSpace(requesterTimeZone)
	if named == "" {
		return nil, false
	}
	location, errorValue := time.LoadLocation(named)
	return location, errorValue == nil
}

func wallClockIn(language string, moment time.Time) string {
	switch toolcontract.NormalizeResponseLanguage(language) {
	case toolcontract.ResponseLanguageKorean:
		return fmt.Sprintf("%d년 %d월 %d일 (%s) %s %d:%02d", moment.Year(), moment.Month(), moment.Day(), koreanWeekdays[moment.Weekday()], koreanHalfOfDay(moment), twelveHourClock(moment), moment.Minute())
	case "ja":
		return fmt.Sprintf("%d年%d月%d日(%s) %d:%02d", moment.Year(), moment.Month(), moment.Day(), japaneseWeekdays[moment.Weekday()], moment.Hour(), moment.Minute())
	case toolcontract.ResponseLanguageEnglish:
		return moment.Format("Mon, Jan 2, 2006, 3:04 PM")
	}
	return moment.Format("2006-01-02 15:04")
}

func koreanHalfOfDay(moment time.Time) string {
	if moment.Hour() < 12 {
		return "오전"
	}
	return "오후"
}

func twelveHourClock(moment time.Time) int {
	if hour := moment.Hour() % 12; hour != 0 {
		return hour
	}
	return 12
}

func utcOffsetOf(moment time.Time) string {
	_, offsetSeconds := moment.Zone()
	if offsetSeconds == 0 {
		return "UTC"
	}
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours, minutes := offsetSeconds/3600, offsetSeconds%3600/60
	if minutes == 0 {
		return fmt.Sprintf("UTC%s%d", sign, hours)
	}
	return fmt.Sprintf("UTC%s%d:%02d", sign, hours, minutes)
}
