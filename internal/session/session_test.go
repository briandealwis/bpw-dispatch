package session

import (
	"testing"
	"time"
)

func TestCurrent(t *testing.T) {
	cases := []struct {
		hour int
		want Session
	}{
		{0, Morning},
		{11, Morning},
		{11, Morning},
		{12, Afternoon},
		{13, Afternoon},
		{23, Afternoon},
	}
	for _, c := range cases {
		now := time.Date(2026, 9, 10, c.hour, 30, 0, 0, time.UTC)
		if got := Current(now); got != c.want {
			t.Errorf("Current(hour=%d) = %v, want %v", c.hour, got, c.want)
		}
	}
}

func TestDateKey(t *testing.T) {
	now := time.Date(2026, 9, 9, 23, 59, 0, 0, time.UTC)
	if got := DateKey(now); got != "2026-09-09" {
		t.Errorf("DateKey = %q, want 2026-09-09", got)
	}
}

func TestWeekdayName(t *testing.T) {
	want := map[time.Weekday]string{
		time.Sunday: "sun", time.Monday: "mon", time.Tuesday: "tue",
		time.Wednesday: "wed", time.Thursday: "thu", time.Friday: "fri", time.Saturday: "sat",
	}
	// Start from a known Sunday (2026-09-06) and walk a full week.
	day := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	if day.Weekday() != time.Sunday {
		t.Fatalf("test setup error: %v is not a Sunday", day)
	}
	for i := 0; i < 7; i++ {
		d := day.AddDate(0, 0, i)
		if got := WeekdayName(d); got != want[d.Weekday()] {
			t.Errorf("WeekdayName(%v) = %q, want %q", d, got, want[d.Weekday()])
		}
	}
}
