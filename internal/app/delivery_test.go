package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/briandealwis/bpw-dispatch/internal/state"
)

var errNtfyDown = fakeErr("ntfy unreachable")

func TestDelivery_FailedStatusIsRetriedNextRun(t *testing.T) {
	h := newFailureHarness()

	h.main.err = errNtfyDown
	h.runAt(9, 0)
	if ks := h.app.State.Kids["kid1"]; ks.Session.Sent {
		t.Fatalf("an undelivered status must not be recorded as sent: %+v", ks.Session)
	}

	h.main.err = nil
	h.runAt(9, 5)
	assertTexts(t, "main", h.main, allClear)

	h.runAt(9, 10) // delivered now, so not sent again
	assertTexts(t, "main", h.main, allClear)
}

func TestDelivery_FailedScheduleChangeAlertIsRetriedNextRun(t *testing.T) {
	h := newFailureHarness()
	h.runAt(9, 0)

	h.app.State.Kids["kid1"].ScheduleDate = "2026-09-08" // force a re-fetch
	h.sf.schedule = &state.Schedule{Morning: &state.Leg{Bus: "141", PickupTime: "7:58 AM"}, Afternoon: schedule140().Afternoon}
	h.main.err = errNtfyDown
	h.runAt(9, 5)

	h.main.err = nil
	h.runAt(9, 10)
	changes := 0
	for _, m := range h.main.texts() {
		if strings.HasPrefix(m, "Example School: schedule changed") {
			changes++
		}
	}
	if changes != 1 {
		t.Errorf("expected the schedule-change alert to be delivered once on retry; got %q", h.main.texts())
	}
}

func TestDelivery_FailedErrorChannelMessageIsRetriedNextRun(t *testing.T) {
	h := newFailureHarness()
	h.af.err = errAlertsDown

	h.errs.err = errNtfyDown
	h.runAt(9, 0)
	h.errs.err = nil
	h.runAt(9, 5)

	assertTexts(t, "errors", h.errs, alertsDown)
}

func TestDelivery_FailedStaleNoticeIsRetriedNextRun(t *testing.T) {
	h := newFailureHarness()
	h.af.err = errAlertsDown

	h.runAt(9, 0)
	h.main.err = errNtfyDown
	h.runAt(9, 15) // stale, but main is unreachable
	h.main.err = nil
	h.runAt(9, 20)

	stale := "Example School, 140: Unable to check bus status since 09:00 (alerts down)"
	if !slices.Equal(h.main.texts(), []string{stale}) {
		t.Errorf("main got %q, want the stale notice delivered once on retry", h.main.texts())
	}
}
