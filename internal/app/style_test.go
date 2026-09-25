package app

import (
	"slices"
	"testing"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

func TestStatusStyle(t *testing.T) {
	cases := []struct {
		name    string
		actions []string
		want    style
	}{
		{"no alerts", nil, styleAllClear},
		{"delay", []string{"Bus Delayed - 10 to 19 minutes"}, styleBusAlert},
		{"English cancellation", []string{"Route Cancelled"}, styleCancellation},
		{"US spelling", []string{"Run canceled due to weather"}, styleCancellation},
		{"French cancellation", []string{"Route Annulée"}, styleCancellation},
		{"cancellation outranks delay", []string{"Bus Delayed", "Annulation - PM run"}, styleCancellation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var alerts []alertsapi.Alert
			for _, a := range c.actions {
				alerts = append(alerts, alertsapi.Alert{Action: a})
			}
			got := statusStyle(alerts)
			if got.priority != c.want.priority || !slices.Equal(got.tags, c.want.tags) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func assertStyle(t *testing.T, what string, m notify.Message, want style) {
	t.Helper()
	if m.Priority != want.priority || !slices.Equal(m.Tags, want.tags) {
		t.Errorf("%s (%q): priority %d tags %v, want priority %d tags %v", what, m.Text, m.Priority, m.Tags, want.priority, want.tags)
	}
}

// TestRun_MessagesAreStyled checks the priority/tags actually attached to
// each kind of message as it goes out, not just the style table.
func TestRun_MessagesAreStyled(t *testing.T) {
	h := newFailureHarness()

	h.runAt(9, 0) // all-clear
	h.af.alerts = alertsDelayed
	h.runAt(9, 5) // delay
	h.af.alerts = []alertsapi.Alert{{Action: "Route Annulée"}}
	h.runAt(9, 10) // cancellation
	h.af.alerts = nil
	h.af.err = errAlertsDown
	h.runAt(9, 15) // failure: error channel only
	h.runAt(9, 30) // failing 15m: main told
	h.af.err = nil
	h.runAt(9, 35) // recovered: all-clear on main, recovery on error channel

	if len(h.main.sent) != 5 {
		t.Fatalf("main got %d messages, want 5: %q", len(h.main.sent), h.main.texts())
	}
	assertStyle(t, "all-clear", h.main.sent[0], styleAllClear)
	assertStyle(t, "delay", h.main.sent[1], styleBusAlert)
	assertStyle(t, "cancellation", h.main.sent[2], styleCancellation)
	assertStyle(t, "unavailable", h.main.sent[3], styleUnavailable)
	assertStyle(t, "all-clear after recovery", h.main.sent[4], styleAllClear)

	if len(h.errs.sent) != 2 {
		t.Fatalf("error channel got %d messages, want 2: %q", len(h.errs.sent), h.errs.texts())
	}
	assertStyle(t, "check failed", h.errs.sent[0], styleCheckFailed)
	assertStyle(t, "check recovered", h.errs.sent[1], styleCheckRecovered)
}

func TestRun_ScheduleChangeIsStyled(t *testing.T) {
	h := newFailureHarness()
	h.runAt(9, 0)
	h.app.State.Kids["kid1"].ScheduleDate = "2026-09-08" // force a re-fetch
	h.sf.schedule = &state.Schedule{Morning: &state.Leg{Bus: "141", PickupTime: "7:58 AM"}, Afternoon: schedule140().Afternoon}
	h.runAt(9, 5)

	for _, m := range h.main.sent {
		if m.ClickURL == portalScheduleURL("example.com") {
			assertStyle(t, "schedule change", m, styleScheduleChange)
			return
		}
	}
	t.Fatalf("no schedule-change message sent; got %q", h.main.texts())
}
