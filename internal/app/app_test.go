package app

import (
	"context"
	"testing"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

type fakeScheduleFetcher struct {
	schedule *state.Schedule
	err      error
	calls    int
}

func (f *fakeScheduleFetcher) FetchSchedule(_ context.Context, _, _, _, _ string) (*state.Schedule, string, error) {
	f.calls++
	if f.err != nil {
		return nil, "", f.err
	}
	return f.schedule, "fake-token", nil
}

type fakeAlertsFetcher struct {
	alerts []alertsapi.Alert
	err    error
	calls  int
}

func (f *fakeAlertsFetcher) FetchAndMatch(_ context.Context, _, _, _ string) ([]alertsapi.Alert, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.alerts, nil
}

type fakeNotifier struct{ sent []notify.Message }

func (f *fakeNotifier) Send(_ context.Context, msg notify.Message) error {
	f.sent = append(f.sent, msg)
	return nil
}

// texts returns just the message text of everything sent, for assertions
// that don't care about ClickURL.
func (f *fakeNotifier) texts() []string {
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.Text
	}
	return out
}

// wed9am and wed2pm are a fixed Wednesday morning/afternoon, and thu9am the
// following day's morning, all in 2026 (2026-09-09 is a Wednesday, verified
// against the same reference date used in session_test.go).
var (
	wed9am = time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)
	wed2pm = time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)
	thu9am = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
)

func testKid() config.Kid {
	return config.Kid{
		ID:        "kid1",
		School:    "Example School",
		Portal:    config.PortalConfig{Domain: "example.com", Username: "u", Password: "p"},
		Notifiers: []string{"main"},
		Sessions: map[string]config.SessionConfig{
			"morning": {Days: []string{"mon", "tue", "wed", "thu", "fri"}},
			"afternoon": {
				Days:         []string{"mon", "tue", "wed", "thu", "fri"},
				DayOverrides: map[string][]string{"wed": {"grandma"}},
			},
		},
	}
}

func newTestApp(t *testing.T, kid config.Kid, sched *state.Schedule, alerts []alertsapi.Alert, now time.Time) (*App, *fakeScheduleFetcher, *fakeAlertsFetcher, *fakeNotifier, *fakeNotifier) {
	t.Helper()
	mainN := &fakeNotifier{}
	grandmaN := &fakeNotifier{}
	scheduleFetcher := &fakeScheduleFetcher{schedule: sched}
	alertsFetcher := &fakeAlertsFetcher{alerts: alerts}

	a := &App{
		Config: &config.Config{
			Notifiers: map[string]config.Notifier{"main": {Type: "ntfy"}, "grandma": {Type: "ntfy"}},
			Kids:      []config.Kid{kid},
		},
		State:     &state.State{Kids: map[string]*state.KidState{}},
		Alerts:    alertsFetcher,
		Portal:    scheduleFetcher,
		Notifiers: map[string]notify.Notifier{"main": mainN, "grandma": grandmaN},
		Now:       func() time.Time { return now },
	}
	return a, scheduleFetcher, alertsFetcher, mainN, grandmaN
}

func schedule140() *state.Schedule {
	return &state.Schedule{
		Morning:   &state.Leg{Bus: "140", PickupTime: "7:58 AM", PickupLocation: "Elm St", DropoffTime: "8:45 AM", DropoffLocation: "School"},
		Afternoon: &state.Leg{Bus: "140", PickupTime: "3:30 PM", PickupLocation: "School", DropoffTime: "3:53 PM", DropoffLocation: "Elm St"},
	}
}

func TestRun_FirstRun_SendsInitialAllClear(t *testing.T) {
	a, sf, af, mainN, grandmaN := newTestApp(t, testKid(), schedule140(), nil, wed9am)

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sf.calls != 1 {
		t.Errorf("schedule fetched %d times, want 1", sf.calls)
	}
	if af.calls != 1 {
		t.Errorf("alerts fetched %d times, want 1", af.calls)
	}
	if len(mainN.sent) != 1 || mainN.sent[0].Text != "Example School, 140: Operating as scheduled" {
		t.Errorf("main notifier got %v", mainN.sent)
	}
	if want := "https://example.com/Alerts"; mainN.sent[0].ClickURL != want {
		t.Errorf("ClickURL = %q, want %q", mainN.sent[0].ClickURL, want)
	}
	if len(grandmaN.sent) != 0 {
		t.Errorf("grandma should not be notified on a non-override day, got %v", grandmaN.sent)
	}

	ks := a.State.Kids["kid1"]
	if ks.ScheduleDate != "2026-09-09" {
		t.Errorf("ScheduleDate = %q", ks.ScheduleDate)
	}
	if !ks.Session.Sent || ks.Session.Name != "morning" {
		t.Errorf("unexpected session state: %+v", ks.Session)
	}
}

func TestRun_SameSessionUnchanged_DoesNotResend(t *testing.T) {
	a, sf, af, mainN, _ := newTestApp(t, testKid(), schedule140(), nil, wed9am)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(mainN.sent) != 1 {
		t.Errorf("expected exactly 1 message across two identical runs, got %v", mainN.sent)
	}
	// The schedule is only fetched once per day, but alerts are checked every run.
	if sf.calls != 1 {
		t.Errorf("schedule fetched %d times, want 1 (once/day)", sf.calls)
	}
	if af.calls != 2 {
		t.Errorf("alerts fetched %d times, want 2 (every run)", af.calls)
	}
}

func TestRun_AlertChangeMidSession_Resends(t *testing.T) {
	kid := testKid()
	mainN := &fakeNotifier{}
	sf := &fakeScheduleFetcher{schedule: schedule140()}
	af := &fakeAlertsFetcher{}
	a := &App{
		Config:    &config.Config{Notifiers: map[string]config.Notifier{"main": {Type: "ntfy"}}, Kids: []config.Kid{kid}},
		State:     &state.State{Kids: map[string]*state.KidState{}},
		Alerts:    af,
		Portal:    sf,
		Notifiers: map[string]notify.Notifier{"main": mainN},
		Now:       func() time.Time { return wed9am },
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	af.alerts = []alertsapi.Alert{{Action: "Bus Delayed - 10 to 19 minutes"}}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(mainN.sent) != 2 {
		t.Fatalf("expected 2 messages (all-clear, then delay), got %v", mainN.sent)
	}
	if mainN.sent[0].Text != "Example School, 140: Operating as scheduled" {
		t.Errorf("first message = %q", mainN.sent[0].Text)
	}
	if mainN.sent[1].Text != "Example School, 140: Bus Delayed - 10 to 19 minutes" {
		t.Errorf("second message = %q", mainN.sent[1].Text)
	}
}

func TestRun_ScheduleChange_SendsChangeAlertToKidDefaultNotifiers(t *testing.T) {
	kid := testKid()
	sched1 := schedule140()
	a, sf, _, mainN, grandmaN := newTestApp(t, kid, sched1, nil, wed9am)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	mainN.sent = nil // clear the initial all-clear message to isolate the schedule-change check

	// Next day: the portal now reports a different bus.
	sf.schedule = &state.Schedule{
		Morning:   &state.Leg{Bus: "141", PickupTime: "7:58 AM", PickupLocation: "Elm St", DropoffTime: "8:45 AM", DropoffLocation: "School"},
		Afternoon: sched1.Afternoon,
	}
	a.Now = func() time.Time { return thu9am }
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	var change *notify.Message
	for i, m := range mainN.sent {
		if m.Text == "Example School: schedule changed - morning: bus now 141 (was 140)" {
			change = &mainN.sent[i]
		}
	}
	if change == nil {
		t.Fatalf("expected a schedule-change message, got %v", mainN.sent)
	}
	if want := "https://example.com/Subscriptions/ChildTransportInfo"; change.ClickURL != want {
		t.Errorf("schedule-change ClickURL = %q, want %q", change.ClickURL, want)
	}
	if len(grandmaN.sent) != 0 {
		t.Errorf("schedule-change alerts use the kid's default notifiers, not the day override; got %v sent to grandma", grandmaN.sent)
	}
}

func TestRun_SessionBoundary_ResetsAndResendsAllClear(t *testing.T) {
	// Use a kid with no day overrides so morning->afternoon is the only
	// thing changing between the two runs.
	kid := testKid()
	kid.Sessions["afternoon"] = config.SessionConfig{Days: []string{"mon", "tue", "wed", "thu", "fri"}}

	a, _, _, mainN, _ := newTestApp(t, kid, schedule140(), nil, wed9am)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.Now = func() time.Time { return wed2pm }
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// One all-clear for the morning session, one for the afternoon session:
	// the afternoon session must not be treated as a duplicate of the
	// morning one just because the message text ("Operating as scheduled")
	// is identical.
	if len(mainN.sent) != 2 {
		t.Fatalf("expected one message per session (morning, afternoon), got %v", mainN.sent)
	}
	if mainN.sent[0].Text != mainN.sent[1].Text {
		t.Errorf("both sessions' all-clear text should read the same: %v", mainN.sent)
	}
}

func TestRun_DayOverride_RoutesAfternoonToGrandmaOnWed(t *testing.T) {
	a, _, _, mainN, grandmaN := newTestApp(t, testKid(), schedule140(), nil, wed2pm)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mainN.sent) != 0 {
		t.Errorf("expected no message to main on Wed afternoon (grandma override), got %v", mainN.sent)
	}
	if len(grandmaN.sent) != 1 {
		t.Errorf("expected exactly one message to grandma on Wed afternoon, got %v", grandmaN.sent)
	}
}

func TestRun_DayOverride_DoesNotApplyOnOtherDays(t *testing.T) {
	fri2pm := time.Date(2026, 9, 11, 14, 0, 0, 0, time.UTC)
	a, _, _, mainN, grandmaN := newTestApp(t, testKid(), schedule140(), nil, fri2pm)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(mainN.sent) != 1 {
		t.Errorf("expected the Friday afternoon message to go to main, got %v", mainN.sent)
	}
	if len(grandmaN.sent) != 0 {
		t.Errorf("grandma override is Wed-only, got %v", grandmaN.sent)
	}
}

func TestRun_InactiveDay_SkipsAlertsButStillChecksSchedule(t *testing.T) {
	kid := testKid()
	kid.Sessions = map[string]config.SessionConfig{
		"morning": {Days: []string{"mon"}}, // not active on our fixed Wednesday
	}
	a, sf, af, mainN, _ := newTestApp(t, kid, schedule140(), nil, wed9am)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sf.calls != 1 {
		t.Errorf("schedule should still be checked once/day regardless of session activity, got %d calls", sf.calls)
	}
	if af.calls != 0 {
		t.Errorf("alerts should not be fetched on an inactive day, got %d calls", af.calls)
	}
	if len(mainN.sent) != 0 {
		t.Errorf("expected no alert on an inactive day, got %v", mainN.sent)
	}
}

func TestRun_ScheduleFetchError_StillChecksAlerts(t *testing.T) {
	kid := testKid()
	mainN := &fakeNotifier{}
	sf := &fakeScheduleFetcher{err: errPortalDown}
	af := &fakeAlertsFetcher{}
	a := &App{
		Config:    &config.Config{Notifiers: map[string]config.Notifier{"main": {Type: "ntfy"}}, Kids: []config.Kid{kid}},
		State:     &state.State{Kids: map[string]*state.KidState{}},
		Alerts:    af,
		Portal:    sf,
		Notifiers: map[string]notify.Notifier{"main": mainN},
		Now:       func() time.Time { return wed9am },
	}

	err := a.Run(context.Background())
	if err == nil {
		t.Fatal("expected Run to report the schedule fetch error")
	}
	if af.calls != 1 {
		t.Errorf("alerts should still be checked even if the schedule fetch failed, got %d calls", af.calls)
	}
	if len(mainN.sent) != 1 {
		t.Errorf("an alert-status message should still be sent, got %v", mainN.sent)
	}
	ks := a.State.Kids["kid1"]
	if ks.ScheduleDate != "" {
		t.Errorf("ScheduleDate should stay empty after a failed fetch, so it retries next run, got %q", ks.ScheduleDate)
	}
}

var errPortalDown = fakeErr("portal unreachable")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
