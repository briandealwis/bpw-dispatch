// Package app orchestrates one run of bpw-dispatch: for each configured
// kid, refresh the daily schedule (alerting on changes), then check for bus
// alerts for the current morning/afternoon session (alerting on any change,
// including the first "operating as scheduled" check of the session).
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/briandealwis/bpw-dispatch/internal/alertsapi"
	"github.com/briandealwis/bpw-dispatch/internal/config"
	"github.com/briandealwis/bpw-dispatch/internal/logging"
	"github.com/briandealwis/bpw-dispatch/internal/notify"
	"github.com/briandealwis/bpw-dispatch/internal/portal"
	"github.com/briandealwis/bpw-dispatch/internal/retry"
	"github.com/briandealwis/bpw-dispatch/internal/session"
	"github.com/briandealwis/bpw-dispatch/internal/state"
)

// ScheduleFetcher logs in to a parent portal and returns a kid's current
// schedule. Implemented by *portal.Client; kept as an interface here so App
// can be tested with a fake.
//
// cachedToken is a BPWebAuth cookie value saved from a previous call; pass
// the empty string on the first call. The returned token should be stored
// in state and passed back on the next call to avoid a full login round-trip.
type ScheduleFetcher interface {
	FetchSchedule(ctx context.Context, domain, username, password, cachedToken string) (*state.Schedule, string, error)
}

// AlertsFetcher fetches and filters bus alerts for a domain. Implemented by
// *alertsapi.Client; kept as an interface here so App can be tested with a
// fake.
type AlertsFetcher interface {
	FetchAndMatch(ctx context.Context, domain, busSubstr, schoolSubstr string) ([]alertsapi.Alert, error)
}

// App holds the wired-up dependencies for one run.
type App struct {
	Config    *config.Config
	State     *state.State
	Alerts    AlertsFetcher
	Portal    ScheduleFetcher
	Notifiers map[string]notify.Notifier
	Now       func() time.Time
	// Retry governs retries of every network call (schedule fetch, alerts
	// check, notifier send). The zero value makes a single attempt.
	Retry retry.Policy
	// Log, when set, receives a line at each step of Run — which kid is
	// being processed, whether the schedule/alerts need checking, and what
	// was (or wasn't) sent — so a run that hangs or takes too long can be
	// traced to where it got stuck.
	Log logging.Logger
}

// New builds an App from config and state, constructing the alerts/portal
// clients and the notifier registry.
func New(cfg *config.Config, st *state.State) (*App, error) {
	notifiers, err := notify.Build(cfg.Notifiers)
	if err != nil {
		return nil, err
	}
	portalClient, err := portal.NewClient()
	if err != nil {
		return nil, fmt.Errorf("building portal client: %w", err)
	}
	return &App{
		Config:    cfg,
		State:     st,
		Alerts:    alertsapi.NewClient(),
		Portal:    portalClient,
		Notifiers: notifiers,
		Now:       time.Now,
		Retry:     retry.Default,
	}, nil
}

// Run processes every configured kid once. Errors for individual kids are
// collected and returned together; one kid's failure doesn't stop the rest.
func (a *App) Run(ctx context.Context) error {
	now := a.Now()
	today := session.DateKey(now)
	sess := session.Current(now)
	weekday := session.WeekdayName(now)
	logging.Logf(a.Log, "run: starting for %s, session=%s, weekday=%s, %d kid(s)", today, sess, weekday, len(a.Config.Kids))

	var errs []error
	for _, kid := range a.Config.Kids {
		logging.Logf(a.Log, "kid %s: starting", kid.ID)
		start := time.Now()
		err := a.runKid(ctx, kid, now, today, sess, weekday)
		if err != nil {
			errs = append(errs, err)
			logging.Logf(a.Log, "kid %s: finished with error after %s: %v", kid.ID, time.Since(start), err)
		} else {
			logging.Logf(a.Log, "kid %s: finished after %s", kid.ID, time.Since(start))
		}
	}
	logging.Logf(a.Log, "run: done")
	return errors.Join(errs...)
}

// runKid refreshes one kid's schedule (at most once a day) and checks their
// bus alerts for the current session.
//
// A failed check is sent to the error notifiers straight away, but the
// kid's regular notifiers keep the result of the last successful check
// until the check has been failing for longer than stale_after. That keeps
// a flaky portal from producing a stream of error / "Operating as scheduled"
// flip-flops on the main channel while still flagging a sustained outage.
func (a *App) runKid(ctx context.Context, kid config.Kid, now time.Time, today string, sess session.Session, weekday string) error {
	ks := a.State.Kids[kid.ID]
	if ks == nil {
		ks = &state.KidState{}
		a.State.Kids[kid.ID] = ks
	}

	// A failure streak left over from a previous day (say, the last run
	// yesterday afternoon failed) says nothing about today; drop it so this
	// morning's first failure isn't treated as already stale.
	ks.ScheduleFailure = dropIfBefore(ks.ScheduleFailure, today)
	ks.AlertsFailure = dropIfBefore(ks.AlertsFailure, today)

	var errs []error
	errNotifiers := a.Config.ErrorNotifiersFor(kid)

	if ks.ScheduleDate != today {
		logging.Logf(a.Log, "kid %s: schedule not yet checked today, fetching", kid.ID)
		var newSched *state.Schedule
		var newToken string
		err := a.withRetry(ctx, "kid "+kid.ID+": schedule fetch", func(ctx context.Context) error {
			var err error
			newSched, newToken, err = a.Portal.FetchSchedule(ctx, kid.Portal.Domain, kid.Portal.Username, kid.Portal.Password, ks.AuthToken)
			return err
		})
		if err != nil {
			logging.Logf(a.Log, "kid %s: schedule fetch failed: %v", kid.ID, err)
			errs = append(errs, fmt.Errorf("kid %s: fetching schedule: %w", kid.ID, err))
			a.recordScheduleFailure(ctx, kid, ks, now, err, errNotifiers, &errs)
		} else {
			logging.Logf(a.Log, "kid %s: schedule fetched", kid.ID)
			changed := ks.Schedule != nil && !ks.Schedule.Equal(newSched)
			if changed {
				logging.Logf(a.Log, "kid %s: schedule changed, sending alert to %v", kid.ID, kid.Notifiers)
				a.send(ctx, kid.Notifiers, styleScheduleChange.message(formatScheduleChange(kid, ks.Schedule, newSched), portalScheduleURL(kid.Portal.Domain)), &errs)
			}
			a.recordScheduleRecovery(ctx, kid, ks, changed, errNotifiers, &errs)
			ks.Schedule = newSched
			ks.ScheduleDate = today
			ks.AuthToken = newToken
		}
	} else {
		logging.Logf(a.Log, "kid %s: schedule already checked today, skipping", kid.ID)
	}

	// Discard alert-tracking state from a previous day or a previous
	// session (e.g. the morning session's state, once it's afternoon).
	if ks.Session == nil || ks.Session.Date != today || ks.Session.Name != string(sess) {
		ks.Session = &state.SessionState{Date: today, Name: string(sess)}
	}

	if !kid.SessionActive(string(sess), weekday) {
		logging.Logf(a.Log, "kid %s: session %s not active on %s, skipping alerts check", kid.ID, sess, weekday)
		return errors.Join(errs...)
	}
	notifierNames := kid.ResolveNotifiers(string(sess), weekday)
	if len(notifierNames) == 0 {
		logging.Logf(a.Log, "kid %s: no notifiers resolved for session %s on %s, skipping alerts check", kid.ID, sess, weekday)
		return errors.Join(errs...)
	}

	leg := scheduleLeg(ks.Schedule, sess)
	busFilter := kid.AlertMatch.Bus
	if busFilter == "" && leg != nil {
		busFilter = leg.Bus
	}

	logging.Logf(a.Log, "kid %s: checking alerts (bus filter=%q, school filter=%q)", kid.ID, busFilter, kid.AlertMatch.School)
	var alerts []alertsapi.Alert
	err := a.withRetry(ctx, "kid "+kid.ID+": alerts check", func(ctx context.Context) error {
		var err error
		alerts, err = a.Alerts.FetchAndMatch(ctx, kid.Portal.Domain, busFilter, kid.AlertMatch.School)
		return err
	})
	if err != nil {
		logging.Logf(a.Log, "kid %s: alerts fetch failed: %v", kid.ID, err)
		errs = append(errs, fmt.Errorf("kid %s: fetching alerts: %w", kid.ID, err))
		a.recordAlertsFailure(ctx, kid, ks, leg, now, err, errNotifiers, notifierNames, &errs)
		return errors.Join(errs...)
	}
	logging.Logf(a.Log, "kid %s: got %d matching alert(s)", kid.ID, len(alerts))
	a.recordAlertsRecovery(ctx, kid, ks, leg, errNotifiers, &errs)

	msg := formatStatusMessage(kid, sess, leg, alerts)
	if !ks.Session.Sent || ks.Session.Failing || msg != ks.Session.LastMessage {
		logging.Logf(a.Log, "kid %s: message changed, sending to %v", kid.ID, notifierNames)
		a.send(ctx, notifierNames, statusStyle(alerts).message(msg, portalAlertsURL(kid.Portal.Domain)), &errs)
		ks.Session.LastMessage = msg
		ks.Session.Sent = true
		ks.Session.Failing = false
	} else {
		logging.Logf(a.Log, "kid %s: message unchanged, not sending", kid.ID)
	}

	return errors.Join(errs...)
}

// recordScheduleFailure reports a failed schedule refresh to the error
// notifiers (unless it's the same failure already reported), and to the
// kid's regular notifiers once, if it has been failing for longer than
// stale_after. The previously fetched schedule is kept as is.
func (a *App) recordScheduleFailure(ctx context.Context, kid config.Kid, ks *state.KidState, now time.Time, err error, errNotifiers []string, errs *[]error) {
	f := ks.ScheduleFailure
	if f == nil {
		f = &state.Failure{Since: now}
		ks.ScheduleFailure = f
	}
	url := portalScheduleURL(kid.Portal.Domain)
	if msg := formatScheduleError(kid, err); msg != f.LastError {
		logging.Logf(a.Log, "kid %s: reporting schedule failure to error notifiers %v", kid.ID, errNotifiers)
		a.send(ctx, errNotifiers, styleCheckFailed.message(msg, url), errs)
		f.LastError = msg
	}
	if f.MainNotified {
		return
	}
	if failing := now.Sub(f.Since); failing < a.Config.StaleAfter {
		logging.Logf(a.Log, "kid %s: schedule refresh failing for %s (< %s), not telling regular notifiers yet", kid.ID, failing, a.Config.StaleAfter)
		return
	}
	logging.Logf(a.Log, "kid %s: schedule refresh failing since %s, telling %v", kid.ID, f.Since.Format("15:04"), kid.Notifiers)
	a.send(ctx, kid.Notifiers, styleUnavailable.message(formatScheduleStale(kid, f.Since, err), url), errs)
	f.MainNotified = true
}

// recordScheduleRecovery ends a schedule failure streak, telling the error
// notifiers, and the regular notifiers too if they were told about the
// failure (unless a schedule-change alert has just gone out, which already
// shows the refresh worked).
func (a *App) recordScheduleRecovery(ctx context.Context, kid config.Kid, ks *state.KidState, changed bool, errNotifiers []string, errs *[]error) {
	f := ks.ScheduleFailure
	if f == nil {
		return
	}
	ks.ScheduleFailure = nil
	url := portalScheduleURL(kid.Portal.Domain)
	msg := formatScheduleRecovered(kid)
	if f.LastError != "" {
		a.send(ctx, errNotifiers, styleCheckRecovered.message(msg, url), errs)
	}
	if f.MainNotified && !changed {
		a.send(ctx, kid.Notifiers, styleRecovered.message(msg+" (no changes)", url), errs)
	}
}

// recordAlertsFailure reports a failed alerts check to the error notifiers
// (unless it's the same failure already reported). The session's regular
// notifiers keep the last successful status until the check has been
// failing for longer than stale_after, at which point they're told once.
func (a *App) recordAlertsFailure(ctx context.Context, kid config.Kid, ks *state.KidState, leg *state.Leg, now time.Time, err error, errNotifiers, notifierNames []string, errs *[]error) {
	f := ks.AlertsFailure
	if f == nil {
		f = &state.Failure{Since: now}
		ks.AlertsFailure = f
	}
	url := portalAlertsURL(kid.Portal.Domain)
	if msg := formatAlertsError(kid, leg, err); msg != f.LastError {
		logging.Logf(a.Log, "kid %s: reporting alerts failure to error notifiers %v", kid.ID, errNotifiers)
		a.send(ctx, errNotifiers, styleCheckFailed.message(msg, url), errs)
		f.LastError = msg
	}
	if ks.Session.Failing {
		return
	}
	if failing := now.Sub(f.Since); failing < a.Config.StaleAfter {
		logging.Logf(a.Log, "kid %s: alerts check failing for %s (< %s), keeping last status %q", kid.ID, failing, a.Config.StaleAfter, ks.Session.LastMessage)
		return
	}
	msg := formatAlertsStale(kid, leg, f.Since, err)
	logging.Logf(a.Log, "kid %s: alerts check failing since %s, telling %v", kid.ID, f.Since.Format("15:04"), notifierNames)
	a.send(ctx, notifierNames, styleUnavailable.message(msg, url), errs)
	ks.Session.LastMessage = msg
	ks.Session.Sent = true
	ks.Session.Failing = true
}

// recordAlertsRecovery ends an alerts failure streak, telling the error
// notifiers if they'd been told about it. The regular notifiers hear about
// recovery via the normal status message (sent because Session.Failing is
// set) only if they'd been told about the failure.
func (a *App) recordAlertsRecovery(ctx context.Context, kid config.Kid, ks *state.KidState, leg *state.Leg, errNotifiers []string, errs *[]error) {
	f := ks.AlertsFailure
	if f == nil {
		return
	}
	ks.AlertsFailure = nil
	if f.LastError != "" {
		a.send(ctx, errNotifiers, styleCheckRecovered.message(formatAlertsRecovered(kid, leg), portalAlertsURL(kid.Portal.Domain)), errs)
	}
}

// dropIfBefore discards a failure streak that started before today.
func dropIfBefore(f *state.Failure, today string) *state.Failure {
	if f != nil && session.DateKey(f.Since) != today {
		return nil
	}
	return f
}

func (a *App) withRetry(ctx context.Context, name string, op func(context.Context) error) error {
	return retry.Do(ctx, a.Retry, a.Log, name, op)
}

func (a *App) send(ctx context.Context, notifierNames []string, msg notify.Message, errs *[]error) {
	for _, name := range notifierNames {
		n, ok := a.Notifiers[name]
		if !ok {
			*errs = append(*errs, fmt.Errorf("notifier %q not found", name))
			continue
		}
		logging.Logf(a.Log, "notifier %s: sending starting", name)
		err := a.withRetry(ctx, "notifier "+name, func(ctx context.Context) error {
			return n.Send(ctx, msg)
		})
		if err != nil {
			logging.Logf(a.Log, "notifier %s: sending failed: %v", name, err)
			*errs = append(*errs, fmt.Errorf("sending via %q: %w", name, err))
		} else {
			logging.Logf(a.Log, "notifier %s: sent", name)
		}
	}
}

// portalScheduleURL is where a parent can see a kid's current pickup/dropoff
// schedule in detail, linked from schedule-change alerts.
func portalScheduleURL(domain string) string {
	return "https://" + domain + "/Subscriptions/ChildTransportInfo"
}

// portalAlertsURL is the portal's Alerts page, linked from bus-alert messages.
func portalAlertsURL(domain string) string {
	return "https://" + domain + "/Alerts"
}

func scheduleLeg(sched *state.Schedule, sess session.Session) *state.Leg {
	if sched == nil {
		return nil
	}
	if sess == session.Morning {
		return sched.Morning
	}
	return sched.Afternoon
}
