// Package session determines which of the two daily sessions (morning/
// afternoon) is current, using a fixed noon local-time cutover.
package session

import "time"

type Session string

const (
	Morning   Session = "morning"
	Afternoon Session = "afternoon"
)

// cutoverHour is the local hour (24h) at which the morning session ends and
// the afternoon session begins.
const cutoverHour = 12

// Current returns the session that `now` falls into.
func Current(now time.Time) Session {
	if now.Hour() < cutoverHour {
		return Morning
	}
	return Afternoon
}

// DateKey formats `now` as the calendar-day key used in state ("2006-01-02").
func DateKey(now time.Time) string {
	return now.Format("2006-01-02")
}

var weekdayNames = map[time.Weekday]string{
	time.Sunday:    "sun",
	time.Monday:    "mon",
	time.Tuesday:   "tue",
	time.Wednesday: "wed",
	time.Thursday:  "thu",
	time.Friday:    "fri",
	time.Saturday:  "sat",
}

// WeekdayName returns the 3-letter lowercase weekday code used in config
// (e.g. "wed") for `now`.
func WeekdayName(now time.Time) string {
	return weekdayNames[now.Weekday()]
}
