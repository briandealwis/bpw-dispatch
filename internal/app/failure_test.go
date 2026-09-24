package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/retry"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

const testStaleAfter = 15 * time.Minute

var alertsDelayed = []alertsapi.Alert{{Action: "Bus Delayed - 10 to 19 minutes"}}

// failureHarness is an App with a separate "errors" channel and a 15m
// stale_after, whose clock and fetch results a test can move between runs.
type failureHarness struct {
	app  *App
	sf   *fakeScheduleFetcher
	af   *fakeAlertsFetcher
	main *fakeNotifier
	errs *fakeNotifier
	now  time.Time
}

func newFailureHarness() *failureHarness {
	kid := testKid()
	kid.Sessions = map[string]config.SessionConfig{"morning": {}, "afternoon": {}} // every day, kid's own notifiers
	h := &failureHarness{
		sf:   &fakeScheduleFetcher{schedule: schedule140()},
		af:   &fakeAlertsFetcher{},
		main: &fakeNotifier{},
		errs: &fakeNotifier{},
	}
	h.app = &App{
		Config: &config.Config{
			StaleAfter:     testStaleAfter,
			ErrorNotifiers: []string{"errors"},
			Notifiers:      map[string]config.Notifier{"main": {Type: "ntfy"}, "errors": {Type: "ntfy"}},
			Kids:           []config.Kid{kid},
		},
		State:     &state.State{Kids: map[string]*state.KidState{}},
		Alerts:    h.af,
		Portal:    h.sf,
		Notifiers: map[string]notify.Notifier{"main": h.main, "errors": h.errs},
		Now:       func() time.Time { return h.now },
	}
	return h
}

// runAt runs the app as if at hh:mm on Wednesday 2026-09-09. Run errors are
// expected whenever a fetch fails, so they're not checked here.
func (h *failureHarness) runAt(hh, mm int) {
	h.now = time.Date(2026, 9, 9, hh, mm, 0, 0, time.UTC)
	_ = h.app.Run(context.Background())
}

func assertTexts(t *testing.T, channel string, n *fakeNotifier, want ...string) {
	t.Helper()
	if got := n.texts(); !slices.Equal(got, want) {
		t.Errorf("%s channel got:\n  %q\nwant:\n  %q", channel, got, want)
	}
}

const (
	allClear    = "Example School, 140: Operating as scheduled"
	alertsDown  = "Example School, 140: Unable to check bus status (alerts down)"
	alertsBack  = "Example School, 140: Bus status check recovered"
	portalDown  = "Example School: Unable to refresh today's schedule (portal unreachable)"
	portalBack  = "Example School: Schedule refresh recovered"
	statusNoBus = "Example School, bus: Operating as scheduled"
)

func TestFailure_BriefAlertsFailureStaysOffMainChannel(t *testing.T) {
	h := newFailureHarness()

	h.runAt(9, 0)
	h.af.err = errAlertsDown
	h.runAt(9, 5)
	h.runAt(9, 10)
	h.af.err = nil
	h.runAt(9, 15)

	// The failure never reached 15 minutes, so main only ever saw the
	// original status — no error, and no repeated "Operating as scheduled"
	// once the check recovered.
	assertTexts(t, "main", h.main, allClear)
	// The error channel hears about the failure once (not on every failing
	// run) and about the recovery.
	assertTexts(t, "errors", h.errs, alertsDown, alertsBack)
	if f := h.app.State.Kids["kid1"].AlertsFailure; f != nil {
		t.Errorf("failure streak should be cleared after recovery, got %+v", f)
	}
}

func TestFailure_SustainedAlertsFailureReachesMainOnce(t *testing.T) {
	h := newFailureHarness()

	h.runAt(9, 0)
	h.af.err = errAlertsDown
	h.runAt(9, 5)
	h.runAt(9, 15) // failing 10m: still within stale_after
	assertTexts(t, "main", h.main, allClear)

	h.runAt(9, 20) // failing 15m: now reported to main
	h.runAt(9, 25) // ...but only once
	stale := "Example School, 140: Unable to check bus status since 09:05 (alerts down)"
	assertTexts(t, "main", h.main, allClear, stale)

	h.af.err = nil
	h.runAt(9, 30) // recovery is sent to main as an all-clear, since main was told
	assertTexts(t, "main", h.main, allClear, stale, allClear)
	assertTexts(t, "errors", h.errs, alertsDown, alertsBack)
}

func TestFailure_RecoveryWithChangedStatusIsStillReported(t *testing.T) {
	h := newFailureHarness()

	h.runAt(9, 0)
	h.af.err = errAlertsDown
	h.runAt(9, 5)
	h.af.err = nil
	h.af.alerts = alertsDelayed
	h.runAt(9, 10)

	assertTexts(t, "main", h.main, allClear, "Example School, 140: Bus Delayed - 10 to 19 minutes")
}

func TestFailure_ChangedErrorIsResentToErrorChannelOnly(t *testing.T) {
	h := newFailureHarness()

	h.af.err = errAlertsDown
	h.runAt(9, 0)
	h.af.err = &timeoutError{}
	h.runAt(9, 5)

	assertTexts(t, "errors", h.errs, alertsDown, "Example School, 140: Unable to check bus status (timed out)")
	assertTexts(t, "main", h.main)
}

func TestFailure_FirstCheckOfSessionFailing_MainWaitsForSuccess(t *testing.T) {
	h := newFailureHarness()

	h.af.err = errAlertsDown
	h.runAt(9, 0)
	assertTexts(t, "main", h.main)

	h.af.err = nil
	h.runAt(9, 5)
	assertTexts(t, "main", h.main, allClear)
}

func TestFailure_StreakCarriesAcrossSessionBoundary(t *testing.T) {
	h := newFailureHarness()

	h.runAt(11, 50)
	h.af.err = errAlertsDown
	h.runAt(11, 55)
	h.runAt(12, 5) // afternoon session, failing 10m
	assertTexts(t, "main", h.main, allClear)

	h.runAt(12, 10) // failing 15m, even though the afternoon session is new
	assertTexts(t, "main", h.main, allClear, "Example School, 140: Unable to check bus status since 11:55 (alerts down)")
}

func TestFailure_StreakFromPreviousDayIsDropped(t *testing.T) {
	h := newFailureHarness()

	h.af.err = errAlertsDown
	h.now = time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC) // Tuesday afternoon
	_ = h.app.Run(context.Background())

	h.runAt(9, 0) // Wednesday morning, still failing
	// Treated as a fresh streak: not yet stale, so nothing to main, and the
	// error channel hears about it again for the new day.
	assertTexts(t, "main", h.main)
	assertTexts(t, "errors", h.errs, alertsDown, alertsDown)
	if since := h.app.State.Kids["kid1"].AlertsFailure.Since; since.Day() != 9 {
		t.Errorf("streak should restart today, got Since=%v", since)
	}
}

func TestFailure_ScheduleFailure(t *testing.T) {
	h := newFailureHarness()

	h.sf.err = errPortalDown
	h.runAt(9, 0)
	// The alerts check still runs (with no known bus yet) and main gets its
	// status; the schedule failure goes only to the error channel.
	assertTexts(t, "main", h.main, statusNoBus)
	assertTexts(t, "errors", h.errs, portalDown)

	h.runAt(9, 15) // failing 15m: main is told, once
	h.runAt(9, 20)
	staleSchedule := "Example School: Unable to refresh today's schedule since 09:00 (portal unreachable)"
	assertTexts(t, "main", h.main, statusNoBus, staleSchedule)
	assertTexts(t, "errors", h.errs, portalDown)

	h.sf.err = nil
	h.runAt(9, 25)
	if !slices.Contains(h.main.texts(), portalBack+" (no changes)") {
		t.Errorf("main was told the refresh was failing, so it should hear it recovered; got %q", h.main.texts())
	}
	assertTexts(t, "errors", h.errs, portalDown, portalBack)
}

func TestFailure_BriefScheduleFailureStaysOffMainChannel(t *testing.T) {
	h := newFailureHarness()

	h.sf.err = errPortalDown
	h.runAt(9, 0)
	h.sf.err = nil
	h.runAt(9, 5)

	for _, m := range h.main.texts() {
		if strings.Contains(m, "refresh today's schedule") || strings.Contains(m, "Schedule refresh") {
			t.Errorf("main should not hear about a schedule failure that recovered within stale_after; got %q", m)
		}
	}
	assertTexts(t, "errors", h.errs, portalDown, portalBack)
}

func TestFailure_ScheduleRecoveryWithChange_SendsChangeNotRecoveryToMain(t *testing.T) {
	h := newFailureHarness()

	h.runAt(9, 0) // yesterday's schedule, as it were
	h.app.State.Kids["kid1"].ScheduleDate = "2026-09-08"
	h.sf.err = errPortalDown
	h.runAt(9, 5)
	h.runAt(9, 20) // stale: main told
	h.sf.err = nil
	h.sf.schedule = &state.Schedule{Morning: &state.Leg{Bus: "141"}, Afternoon: schedule140().Afternoon}
	h.runAt(9, 25)

	texts := h.main.texts()
	if slices.Contains(texts, portalBack+" (no changes)") {
		t.Errorf("a schedule-change alert already shows the refresh worked; got %q", texts)
	}
	found := false
	for _, m := range texts {
		if strings.HasPrefix(m, "Example School: schedule changed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a schedule-change alert, got %q", texts)
	}
}

func TestFailure_RetriesTransientErrorsWithinOneRun(t *testing.T) {
	h := newFailureHarness()
	var waits []time.Duration
	h.app.Retry = retry.Policy{
		MaxRetries:     5,
		InitialBackoff: time.Second,
		Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
	}
	h.af.errSeq = []error{&timeoutError{}, &timeoutError{}, nil}

	h.runAt(9, 0)

	if h.af.calls != 3 {
		t.Errorf("alerts fetched %d times, want 3 (two timeouts, then success)", h.af.calls)
	}
	if want := []time.Duration{time.Second, 2 * time.Second}; !slices.Equal(waits, want) {
		t.Errorf("backoff waits = %v, want %v", waits, want)
	}
	assertTexts(t, "main", h.main, allClear)
	assertTexts(t, "errors", h.errs)
}

func TestFailure_PermanentErrorsAreNotRetried(t *testing.T) {
	h := newFailureHarness()
	h.app.Retry = retry.Policy{
		MaxRetries: 5,
		Sleep:      func(context.Context, time.Duration) error { return nil },
	}
	h.af.err = errAlertsDown // a plain error, not a timeout/network/5xx failure

	h.runAt(9, 0)

	if h.af.calls != 1 {
		t.Errorf("alerts fetched %d times, want 1", h.af.calls)
	}
}

func TestFailure_NoErrorNotifiersConfigured(t *testing.T) {
	h := newFailureHarness()
	h.app.Config.ErrorNotifiers = nil
	h.af.err = errAlertsDown

	h.runAt(9, 0)
	h.runAt(9, 15)

	assertTexts(t, "main", h.main, "Example School, 140: Unable to check bus status since 09:00 (alerts down)")
	assertTexts(t, "errors", h.errs)
}

func TestFailure_KidErrorNotifiersOverrideTopLevel(t *testing.T) {
	h := newFailureHarness()
	h.app.Config.Kids[0].ErrorNotifiers = []string{"main"}
	h.af.err = errAlertsDown

	h.runAt(9, 0)

	assertTexts(t, "main", h.main, alertsDown)
	assertTexts(t, "errors", h.errs)
}
